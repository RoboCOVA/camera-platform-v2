package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yourorg/cam-agent/internal/discovery"
	"github.com/yourorg/cam-agent/internal/frigate"
)

// AgentConfig holds runtime configuration loaded from env or config file.
type AgentConfig struct {
	// Identity
	DeviceID string // read from /etc/cam/device.id, generated on first boot
	SiteID   string // set during provisioning
	OrgID    string // set during provisioning

	// Control plane
	ControlPlaneURL string // e.g. https://api.yourdomain.com
	ControlPlaneKey string // pre-shared key for initial registration

	// Camera credentials to try (in order)
	CameraCredentials []discovery.Credentials

	// Paths
	FrigateConfigPath string
	DataPath          string

	// Network
	DiscoverySubnet string // e.g. "192.168.1.0/24"; empty = use multicast
}

func loadConfig() AgentConfig {
	cfg := AgentConfig{
		DeviceID:          getOrCreateDeviceID("/etc/cam/device.id"),
		ControlPlaneURL:   mustEnv("CAM_CONTROL_URL"),
		ControlPlaneKey:   mustEnv("CAM_CONTROL_KEY"),
		FrigateConfigPath: getEnvOr("CAM_FRIGATE_CONFIG", "/etc/frigate/frigate.yml"),
		DataPath:          getEnvOr("CAM_DATA_PATH", "/data"),
		DiscoverySubnet:   os.Getenv("CAM_SUBNET"), // optional
		// Default credentials to try — override with env or config file
		CameraCredentials: []discovery.Credentials{
			{Username: getEnvOr("CAM_CRED_USER_1", "admin"), Password: getEnvOr("CAM_CRED_PASS_1", "admin")},
			{Username: "admin", Password: "12345"},
			{Username: "admin", Password: "123456"},
			{Username: "root", Password: "pass"},
		},
	}
	return cfg
}

func main() {
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("[agent] starting device=%s", cfg.DeviceID)

	// 1. Start Frigate manager (will start after config is written)
	frigateOpts := frigate.DefaultOptions()
	frigateOpts.DatabasePath = cfg.DataPath + "/frigate.db"
	frigateOpts.MQTTHost = getEnvOr("CAM_MQTT_HOST", "localhost")

	frigateManager := frigate.NewManager(cfg.FrigateConfigPath, cfg.DataPath)

	// 2. Run initial camera discovery
	cameras, err := discoverCameras(ctx, cfg)
	if err != nil {
		log.Printf("[agent] discovery error (continuing): %v", err)
	}
	log.Printf("[agent] discovered %d cameras", len(cameras))
	state := &cameraState{cameras: cameras}

	// 3. Write Frigate config and start it
	frigateConfig := frigate.Generate(cameras, frigateOpts)
	if err := frigate.WriteConfig(frigateConfig, cfg.FrigateConfigPath); err != nil {
		log.Fatalf("[agent] write frigate config: %v", err)
	}

	if err := frigateManager.Start(ctx); err != nil {
		log.Fatalf("[agent] start frigate: %v", err)
	}

	// 4. Register cameras with control plane
	if err := registerCameras(ctx, cfg, state.Get()); err != nil {
		log.Printf("[agent] register cameras: %v", err)
	}

	// 5. Send initial heartbeat and start loop
	if err := sendHeartbeat(ctx, cfg, state.Get()); err != nil {
		log.Printf("[heartbeat] initial error: %v", err)
	}
	go heartbeatLoop(ctx, cfg, state)

	// 6. Start periodic re-discovery (picks up new cameras without restart)
	go rediscoveryLoop(ctx, cfg, state, frigateConfig, frigateOpts, frigateManager)

	// 7. Serve local health endpoint
	go serveHealth(cfg.DeviceID, state)

	log.Println("[agent] running. Press Ctrl+C to stop.")
	<-ctx.Done()
	log.Println("[agent] shutting down...")
	frigateManager.Stop()
}

// discoverCameras runs ONVIF discovery and returns found cameras.
func discoverCameras(ctx context.Context, cfg AgentConfig) ([]discovery.Camera, error) {
	d := discovery.New(cfg.CameraCredentials, 8*time.Second)

	discCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if cfg.DiscoverySubnet != "" {
		log.Printf("[discovery] scanning subnet %s", cfg.DiscoverySubnet)
		return d.DiscoverSubnet(discCtx, cfg.DiscoverySubnet)
	}

	log.Println("[discovery] running WS-Discovery multicast...")
	return d.Discover(discCtx)
}

// heartbeatLoop posts device status to the control plane every 30s.
func heartbeatLoop(ctx context.Context, cfg AgentConfig, state *cameraState) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendHeartbeat(ctx, cfg, state.Get()); err != nil {
				log.Printf("[heartbeat] error: %v", err)
			}
		}
	}
}

// rediscoveryLoop re-scans for cameras every 5 minutes.
// Adds new cameras to Frigate dynamically without a full restart.
func rediscoveryLoop(
	ctx context.Context,
	cfg AgentConfig,
	state *cameraState,
	frigateConfig *frigate.Config,
	opts frigate.GeneratorOptions,
	mgr *frigate.Manager,
) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fresh, err := discoverCameras(ctx, cfg)
			if err != nil {
				log.Printf("[rediscovery] error: %v", err)
				continue
			}

			current := state.Get()
			added, removed := frigate.DiffCameras(current, fresh)
			if len(added) == 0 && len(removed) == 0 {
				continue
			}

			log.Printf("[rediscovery] +%d -%d cameras", len(added), len(removed))

			// Regenerate and reload Frigate config
			newConfig := frigate.Generate(fresh, opts)
			if err := frigate.WriteConfig(newConfig, cfg.FrigateConfigPath); err != nil {
				log.Printf("[rediscovery] write config: %v", err)
				continue
			}

			if err := mgr.Reload(ctx); err != nil {
				log.Printf("[rediscovery] reload frigate: %v", err)
			}

			// Update control plane with full camera list (new + existing)
			_ = registerCameras(ctx, cfg, fresh)

			state.Set(fresh)
			frigateConfig = newConfig
		}
	}
}

// ─── Control plane API calls ──────────────────────────────────────────────────

type heartbeatPayload struct {
	DeviceID  string         `json:"device_id"`
	SiteID    string         `json:"site_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Cameras   []cameraStatus `json:"cameras"`
	AgentVer  string         `json:"agent_version"`
}

type cameraStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Online bool   `json:"online"`
}

func sendHeartbeat(ctx context.Context, cfg AgentConfig, cameras []discovery.Camera) error {
	statuses := make([]cameraStatus, len(cameras))
	for i, c := range cameras {
		statuses[i] = cameraStatus{
			ID:     c.ID,
			Name:   c.Name,
			IP:     c.IP,
			Online: true,
		}
	}

	payload := heartbeatPayload{
		DeviceID:  cfg.DeviceID,
		SiteID:    cfg.SiteID,
		Timestamp: time.Now().UTC(),
		Cameras:   statuses,
		AgentVer:  "0.1.0",
	}

	return postJSON(ctx, cfg.ControlPlaneURL+"/api/devices/heartbeat", cfg.ControlPlaneKey, payload)
}

type registerCameraPayload struct {
	DeviceID string               `json:"device_id"`
	SiteID   string               `json:"site_id,omitempty"`
	Cameras  []cameraRegistration `json:"cameras"`
}

type cameraRegistration struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Manufacturer  string `json:"manufacturer"`
	Model         string `json:"model"`
	Serial        string `json:"serial"`
	IP            string `json:"ip"`
	MainStreamURL string `json:"main_stream_url"`
	SubStreamURL  string `json:"sub_stream_url"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
}

func registerCameras(ctx context.Context, cfg AgentConfig, cameras []discovery.Camera) error {
	regs := make([]cameraRegistration, len(cameras))
	for i, c := range cameras {
		regs[i] = cameraRegistration{
			ID:            c.ID,
			Name:          c.Name,
			Manufacturer:  c.Manufacturer,
			Model:         c.Model,
			Serial:        c.Serial,
			IP:            c.IP,
			MainStreamURL: c.MainStream.URL,
			SubStreamURL:  c.SubStream.URL,
			Width:         c.MainStream.Width,
			Height:        c.MainStream.Height,
		}
	}

	payload := registerCameraPayload{
		DeviceID: cfg.DeviceID,
		SiteID:   cfg.SiteID,
		Cameras:  regs,
	}

	return postJSON(ctx, cfg.ControlPlaneURL+"/api/devices/cameras", cfg.ControlPlaneKey, payload)
}

func postJSON(ctx context.Context, url, key string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url,
		jsonReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Key", key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("control plane returned %d", resp.StatusCode)
	}
	return nil
}

// ─── Local health server ──────────────────────────────────────────────────────

func serveHealth(deviceID string, state *cameraState) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		cams := state.Get()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"device_id": deviceID,
			"cameras":   len(cams),
			"status":    "ok",
			"time":      time.Now().UTC(),
		})
	})
	log.Fatal(http.ListenAndServe(":8090", nil))
}

type cameraState struct {
	mu      sync.RWMutex
	cameras []discovery.Camera
}

func (s *cameraState) Get() []discovery.Camera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]discovery.Camera, len(s.cameras))
	copy(out, s.cameras)
	return out
}

func (s *cameraState) Set(cams []discovery.Camera) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cameras = make([]discovery.Camera, len(cams))
	copy(s.cameras, cams)
}

// ─── Utilities ────────────────────────────────────────────────────────────────

func getOrCreateDeviceID(path string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		id := string(data)
		if len(id) >= 36 {
			return id[:36]
		}
	}
	// Generate new device ID
	id := fmt.Sprintf("dev-%d", time.Now().UnixNano())
	_ = os.MkdirAll("/etc/cam", 0755)
	_ = os.WriteFile(path, []byte(id), 0640)
	return id
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func jsonReader(data []byte) *strings.Reader {
	return strings.NewReader(string(data))
}
