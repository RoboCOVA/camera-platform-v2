// Package frigate generates and manages Frigate NVR configuration.
// It takes discovered cameras and produces a valid frigate.yml,
// then manages Frigate as a subprocess with hot-reload support.
package frigate

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yourorg/cam-agent/internal/discovery"
)

// Config is the top-level Frigate configuration structure.
// Only fields we set programmatically are included — Frigate has many more.
type Config struct {
	MQTT      MQTTConfig               `yaml:"mqtt"`
	Database  DatabaseConfig           `yaml:"database"`
	Detectors map[string]DetectorConfig `yaml:"detectors"`
	Record    RecordConfig             `yaml:"record"`
	Snapshots SnapshotConfig           `yaml:"snapshots"`
	Cameras   map[string]CameraConfig  `yaml:"cameras"`
	Logger    LoggerConfig             `yaml:"logger"`
}

type MQTTConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	TopicPrefix string `yaml:"topic_prefix"`
	ClientID    string `yaml:"client_id,omitempty"`
	User        string `yaml:"user,omitempty"`
	Password    string `yaml:"password,omitempty"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type DetectorConfig struct {
	Type   string `yaml:"type"`
	Device string `yaml:"device,omitempty"` // for coral: usb or pci
}

type RecordConfig struct {
	Enabled  bool               `yaml:"enabled"`
	Retain   RetainConfig       `yaml:"retain"`
	Events   RecordEventsConfig `yaml:"events"`
}

type RetainConfig struct {
	Days int    `yaml:"days"`
	Mode string `yaml:"mode"` // all | motion | active_objects
}

type RecordEventsConfig struct {
	Retain RetainConfig `yaml:"retain"`
}

type SnapshotConfig struct {
	Enabled   bool         `yaml:"enabled"`
	Timestamp bool         `yaml:"timestamp"`
	Bounding  bool         `yaml:"bounding_box"`
	Retain    RetainConfig `yaml:"retain"`
}

type CameraConfig struct {
	FFMPEG  FFmpegConfig  `yaml:"ffmpeg"`
	Detect  DetectConfig  `yaml:"detect"`
	Record  RecordConfig  `yaml:"record,omitempty"`
	Objects ObjectConfig  `yaml:"objects"`
	Motion  MotionConfig  `yaml:"motion"`
}

type FFmpegConfig struct {
	Inputs        []FFmpegInput `yaml:"inputs"`
	GlobalArgs    string        `yaml:"global_args,omitempty"`
	HWAccelArgs   string        `yaml:"hwaccel_args,omitempty"`
}

type FFmpegInput struct {
	Path   string   `yaml:"path"`
	Roles  []string `yaml:"roles"`
	Input  string   `yaml:"input_args,omitempty"`
}

type DetectConfig struct {
	Width   int  `yaml:"width"`
	Height  int  `yaml:"height"`
	FPS     int  `yaml:"fps"`
	Enabled bool `yaml:"enabled"`
}

type ObjectConfig struct {
	Track  []string           `yaml:"track"`
	Filters map[string]Filter `yaml:"filters,omitempty"`
}

type Filter struct {
	MinScore    float64 `yaml:"min_score,omitempty"`
	MinArea     int     `yaml:"min_area,omitempty"`
	MaxArea     int     `yaml:"max_area,omitempty"`
}

type MotionConfig struct {
	Threshold       int  `yaml:"threshold"`
	ContourArea     int  `yaml:"contour_area"`
	FrameAlpha      float64 `yaml:"frame_alpha"`
	FrameHeight     int  `yaml:"frame_height"`
	ImproveContrast bool `yaml:"improve_contrast"`
}

type LoggerConfig struct {
	Default string            `yaml:"default"`
	Logs    map[string]string `yaml:"logs,omitempty"`
}

// GeneratorOptions controls how configs are generated.
type GeneratorOptions struct {
	MQTTHost       string
	MQTTPort       int
	RecordingPath  string // e.g. /data/recordings
	DatabasePath   string // e.g. /data/frigate.db
	RetentionDays  int
	DetectorType   string // cpu | coral | hailo
	DetectorDevice string // usb | pci (for coral)

	// Default objects to track on all cameras
	TrackObjects []string
}

// DefaultOptions returns sensible production defaults.
func DefaultOptions() GeneratorOptions {
	return GeneratorOptions{
		MQTTHost:       "localhost",
		MQTTPort:       1883,
		RecordingPath:  "/data/recordings",
		DatabasePath:   "/data/frigate.db",
		RetentionDays:  30,
		DetectorType:   "cpu",
		TrackObjects:   []string{"person", "car", "bicycle", "motorcycle"},
	}
}

// Generate produces a Frigate Config from a list of discovered cameras.
func Generate(cameras []discovery.Camera, opts GeneratorOptions) *Config {
	cfg := &Config{
		MQTT: MQTTConfig{
			Host:        opts.MQTTHost,
			Port:        opts.MQTTPort,
			TopicPrefix: "frigate",
			ClientID:    "frigate",
		},
		Database: DatabaseConfig{
			Path: opts.DatabasePath,
		},
		Detectors: buildDetectors(opts),
		Record: RecordConfig{
			Enabled: true,
			Retain: RetainConfig{
				Days: opts.RetentionDays,
				Mode: "all",
			},
			Events: RecordEventsConfig{
				Retain: RetainConfig{Days: opts.RetentionDays, Mode: "active_objects"},
			},
		},
		Snapshots: SnapshotConfig{
			Enabled:   true,
			Timestamp: true,
			Bounding:  true,
			Retain:    RetainConfig{Days: opts.RetentionDays},
		},
		Logger: LoggerConfig{
			Default: "warning",
			Logs:    map[string]string{"frigate.event": "debug"},
		},
		Cameras: make(map[string]CameraConfig),
	}

	for _, cam := range cameras {
		name := sanitizeCameraName(cam.Name, cam.ID)
		cfg.Cameras[name] = buildCameraConfig(cam, opts)
	}

	return cfg
}

// buildDetectors sets up AI detection based on available hardware.
func buildDetectors(opts GeneratorOptions) map[string]DetectorConfig {
	d := map[string]DetectorConfig{}

	switch opts.DetectorType {
	case "coral":
		d["coral"] = DetectorConfig{
			Type:   "edgetpu",
			Device: opts.DetectorDevice,
		}
	case "hailo":
		d["hailo"] = DetectorConfig{Type: "hailo8l"}
	case "openvino":
		d["ov"] = DetectorConfig{Type: "openvino"}
	default:
		// CPU fallback — works everywhere, slower
		d["cpu1"] = DetectorConfig{Type: "cpu"}
		d["cpu2"] = DetectorConfig{Type: "cpu"}
	}

	return d
}

// buildCameraConfig maps a discovered camera to a Frigate camera config.
func buildCameraConfig(cam discovery.Camera, opts GeneratorOptions) CameraConfig {
	// Detect stream: use substream if available (lower res = faster inference)
	// Record stream: always use main stream (full resolution)
	detectWidth, detectHeight := cam.SubStream.Width, cam.SubStream.Height
	if detectWidth == 0 {
		detectWidth, detectHeight = cam.MainStream.Width, cam.MainStream.Height
	}
	// Frigate detect max is 1920x1080 — cap it
	if detectWidth > 1920 {
		detectWidth = 1920
		detectHeight = 1080
	}

	inputs := []FFmpegInput{
		{
			Path:  cam.MainStream.URL,
			Roles: []string{"record"},
			Input: "-avoid_negative_ts make_zero -fflags +genpts+discardcorrupt -use_wallclock_as_timestamps 1",
		},
	}

	// Only add a separate detect input if substream differs from main
	if cam.SubStream.URL != "" && cam.SubStream.URL != cam.MainStream.URL {
		inputs = append(inputs, FFmpegInput{
			Path:  cam.SubStream.URL,
			Roles: []string{"detect"},
		})
	} else {
		// Reuse main stream for detect (Frigate will scale down)
		inputs[0].Roles = append(inputs[0].Roles, "detect")
	}

	trackObjects := opts.TrackObjects
	if len(trackObjects) == 0 {
		trackObjects = []string{"person", "car"}
	}

	return CameraConfig{
		FFMPEG: FFmpegConfig{
			Inputs: inputs,
			// Hardware decode where available — comment out if causing issues
			HWAccelArgs: "preset-vaapi", // Intel/AMD GPU decode; use preset-nvidia for NVIDIA
		},
		Detect: DetectConfig{
			Width:   detectWidth,
			Height:  detectHeight,
			FPS:     5, // 5fps is plenty for detection; saves CPU
			Enabled: true,
		},
		Objects: ObjectConfig{
			Track: trackObjects,
			Filters: map[string]Filter{
				"person": {MinScore: 0.6, MinArea: 1000},
				"car":    {MinScore: 0.6, MinArea: 5000},
			},
		},
		Motion: MotionConfig{
			Threshold:       25,
			ContourArea:     100,
			FrameAlpha:      0.01,
			FrameHeight:     100,
			ImproveContrast: true,
		},
	}
}

// WriteConfig serializes the config to YAML and writes it to disk.
func WriteConfig(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}

	// Write to temp file then rename — atomic on Linux
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

// DiffCameras returns cameras added or removed compared to previous list.
func DiffCameras(prev, next []discovery.Camera) (added, removed []discovery.Camera) {
	prevMap := map[string]bool{}
	for _, c := range prev {
		prevMap[c.ID] = true
	}
	nextMap := map[string]bool{}
	for _, c := range next {
		nextMap[c.ID] = true
	}
	for _, c := range next {
		if !prevMap[c.ID] {
			added = append(added, c)
		}
	}
	for _, c := range prev {
		if !nextMap[c.ID] {
			removed = append(removed, c)
		}
	}
	return
}

// sanitizeCameraName converts a camera name to a valid Frigate camera key.
// Frigate camera names must be lowercase alphanumeric + underscore.
func sanitizeCameraName(name, id string) string {
	// Use first 8 chars of ID to ensure uniqueness
	shortID := strings.ReplaceAll(id, "-", "")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32 // lowercase
		}
		return '_'
	}, name)
	// Collapse multiple underscores
	for strings.Contains(safe, "__") {
		safe = strings.ReplaceAll(safe, "__", "_")
	}
	safe = strings.Trim(safe, "_")
	return fmt.Sprintf("%s_%s", safe, shortID)
}

// ─── Frigate process manager ──────────────────────────────────────────────────

// Manager starts and supervises a Frigate instance.
// Supports two modes:
//   - "subprocess" (default): runs `python3 -m frigate` directly
//   - "docker": expects Frigate to run as a separate Docker container;
//     the Manager only communicates via Frigate's HTTP API.
//
// Set the mode via NewManager's optional parameter or the CAM_FRIGATE_MODE env var.
type Manager struct {
	configPath string
	dataPath   string
	mode       string // "subprocess" or "docker"
	mu         sync.Mutex
	cmd        *exec.Cmd
	cancel     context.CancelFunc
}

// NewManager creates a Frigate Manager.
// mode is read from CAM_FRIGATE_MODE env var: "docker" or "subprocess" (default).
func NewManager(configPath, dataPath string) *Manager {
	mode := os.Getenv("CAM_FRIGATE_MODE")
	if mode == "" {
		mode = "subprocess"
	}
	return &Manager{
		configPath: configPath,
		dataPath:   dataPath,
		mode:       mode,
	}
}

// Start launches Frigate. In subprocess mode, it starts and auto-restarts
// the process. In docker mode, it verifies the Frigate API is reachable.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.mode == "docker" {
		fmt.Println("[frigate] mode=docker — skipping subprocess, using external container")
		// In docker mode, Frigate is managed externally (e.g., via start-frigate.sh).
		// Just verify it's reachable (non-blocking; it may not be up yet).
		go func() {
			for i := 0; i < 30; i++ {
				if _, err := frigateAPI(ctx, "GET", "http://localhost:5000/api/version", nil); err == nil {
					fmt.Println("[frigate] docker container is reachable at http://localhost:5000")
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
			fmt.Println("[frigate] warning: docker container not reachable after 150s — run start-frigate.sh")
		}()
		return nil
	}

	if m.cmd != nil {
		return fmt.Errorf("frigate already running")
	}

	go m.supervise(ctx)
	return nil
}

// Reload signals Frigate to reload its config (sends SIGHUP equivalent via API).
// Frigate 0.13+ supports live config reload via its API.
func (m *Manager) Reload(ctx context.Context) error {
	resp, err := frigateAPI(ctx, "POST", "http://localhost:5000/api/config/save", nil)
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	_ = resp
	return nil
}

// Stop gracefully shuts down Frigate.
// In docker mode this is a no-op (the container is managed externally).
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) supervise(parent context.Context) {
	for {
		select {
		case <-parent.Done():
			return
		default:
		}

		ctx, cancel := context.WithCancel(parent)
		m.mu.Lock()
		m.cancel = cancel
		m.cmd = exec.CommandContext(ctx, "python3", "-m", "frigate")
		m.cmd.Env = append(os.Environ(),
			"CONFIG_FILE="+m.configPath,
		)
		m.cmd.Stdout = os.Stdout
		m.cmd.Stderr = os.Stderr
		m.mu.Unlock()

		fmt.Println("[frigate] starting...")
		if err := m.cmd.Run(); err != nil {
			fmt.Printf("[frigate] exited: %v\n", err)
		}

		m.mu.Lock()
		m.cmd = nil
		m.mu.Unlock()

		select {
		case <-parent.Done():
			return
		case <-time.After(5 * time.Second):
			fmt.Println("[frigate] restarting...")
		}
	}
}

func frigateAPI(ctx context.Context, method, url string, body interface{}) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return buf[:n], nil
}
