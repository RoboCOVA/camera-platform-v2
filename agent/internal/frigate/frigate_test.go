package frigate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/yourorg/cam-agent/internal/discovery"
	"github.com/yourorg/cam-agent/internal/frigate"
)

// ─── Config generation tests ──────────────────────────────────────────────────

func TestGenerate_SingleCamera(t *testing.T) {
	cameras := []discovery.Camera{
		{
			ID:           "cam-001",
			Name:         "Hikvision DS-2CD2143G2",
			Manufacturer: "Hikvision",
			Model:        "DS-2CD2143G2",
			Serial:       "SN001",
			IP:           "192.168.1.100",
			MainStream: discovery.RTSPStream{
				URL: "rtsp://admin:pass@192.168.1.100/Streaming/Channels/101",
				Width: 2688, Height: 1520, FPS: 25, Codec: "H265",
			},
			SubStream: discovery.RTSPStream{
				URL: "rtsp://admin:pass@192.168.1.100/Streaming/Channels/102",
				Width: 640, Height: 360, FPS: 15, Codec: "H264",
			},
		},
	}

	opts := frigate.DefaultOptions()
	cfg := frigate.Generate(cameras, opts)

	// Must have MQTT config
	if cfg.MQTT.Host == "" {
		t.Error("MQTT host should not be empty")
	}
	if cfg.MQTT.TopicPrefix != "frigate" {
		t.Errorf("topic prefix: got %q, want %q", cfg.MQTT.TopicPrefix, "frigate")
	}

	// Must have exactly one camera
	if len(cfg.Cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cfg.Cameras))
	}

	// Camera name must be sanitized
	var camKey string
	for k := range cfg.Cameras {
		camKey = k
	}
	if camKey == "" {
		t.Error("camera key should not be empty")
	}
	// Must be lowercase and contain only safe chars
	for _, ch := range camKey {
		if !isAlphanumOrUnderscore(ch) {
			t.Errorf("camera key %q contains unsafe char %q", camKey, ch)
		}
	}

	cam := cfg.Cameras[camKey]

	// Must have both record and detect inputs
	hasRecord, hasDetect := false, false
	for _, inp := range cam.FFMPEG.Inputs {
		for _, role := range inp.Roles {
			if role == "record" { hasRecord = true }
			if role == "detect" { hasDetect = true }
		}
	}
	if !hasRecord {
		t.Error("camera must have a record input")
	}
	if !hasDetect {
		t.Error("camera must have a detect input")
	}

	// Detect resolution should use substream (640x360)
	if cam.Detect.Width != 640 {
		t.Errorf("detect width: got %d, want 640 (substream)", cam.Detect.Width)
	}

	// Must track person and car at minimum
	tracked := map[string]bool{}
	for _, obj := range cam.Objects.Track {
		tracked[obj] = true
	}
	if !tracked["person"] {
		t.Error("must track person")
	}
	if !tracked["car"] {
		t.Error("must track car")
	}
}

func TestGenerate_MultipleCamera(t *testing.T) {
	cameras := makeCameras(5)
	cfg := frigate.Generate(cameras, frigate.DefaultOptions())

	if len(cfg.Cameras) != 5 {
		t.Errorf("expected 5 cameras, got %d", len(cfg.Cameras))
	}

	// All camera keys must be unique
	keys := map[string]int{}
	for k := range cfg.Cameras {
		keys[k]++
	}
	for k, count := range keys {
		if count > 1 {
			t.Errorf("duplicate camera key %q (%d times)", k, count)
		}
	}
}

func TestGenerate_NoSubstream(t *testing.T) {
	// Camera with only a main stream (no substream)
	cameras := []discovery.Camera{
		{
			ID:    "cam-001",
			Name:  "Basic Camera",
			IP:    "192.168.1.1",
			MainStream: discovery.RTSPStream{
				URL: "rtsp://192.168.1.1/stream", Width: 1920, Height: 1080,
			},
			// SubStream is zero-value (URL == "")
		},
	}

	cfg := frigate.Generate(cameras, frigate.DefaultOptions())
	if len(cfg.Cameras) != 1 {
		t.Fatal("expected 1 camera")
	}
	for _, cam := range cfg.Cameras {
		// Should fall back to main stream for both roles
		hasRecord, hasDetect := false, false
		for _, inp := range cam.FFMPEG.Inputs {
			for _, role := range inp.Roles {
				if role == "record" { hasRecord = true }
				if role == "detect" { hasDetect = true }
			}
		}
		if !hasRecord || !hasDetect {
			t.Errorf("camera without substream must use main stream for both roles: record=%v detect=%v", hasRecord, hasDetect)
		}
	}
}

func TestGenerate_DetectResolutionCap(t *testing.T) {
	// Camera with 4K main stream, no substream — detect should be capped at 1920x1080
	cameras := []discovery.Camera{
		{
			ID:   "cam-4k",
			Name: "4K Camera",
			IP:   "192.168.1.1",
			MainStream: discovery.RTSPStream{
				URL: "rtsp://192.168.1.1/stream", Width: 3840, Height: 2160,
			},
		},
	}

	cfg := frigate.Generate(cameras, frigate.DefaultOptions())
	for _, cam := range cfg.Cameras {
		if cam.Detect.Width > 1920 {
			t.Errorf("detect width should be capped at 1920, got %d", cam.Detect.Width)
		}
	}
}

func TestGenerate_RecordingEnabled(t *testing.T) {
	cfg := frigate.Generate(makeCameras(1), frigate.DefaultOptions())
	if !cfg.Record.Enabled {
		t.Error("recording must be enabled by default")
	}
	if cfg.Record.Retain.Days <= 0 {
		t.Errorf("retention days must be positive, got %d", cfg.Record.Retain.Days)
	}
}

func TestGenerate_SnapshotsEnabled(t *testing.T) {
	cfg := frigate.Generate(makeCameras(1), frigate.DefaultOptions())
	if !cfg.Snapshots.Enabled {
		t.Error("snapshots must be enabled by default")
	}
}

func TestGenerate_DetectorConfig(t *testing.T) {
	cases := []struct {
		detectorType string
		wantKey      string
	}{
		{"cpu", "cpu1"},
		{"coral", "coral"},
		{"hailo", "hailo"},
	}
	for _, c := range cases {
		opts := frigate.DefaultOptions()
		opts.DetectorType = c.detectorType
		cfg := frigate.Generate(makeCameras(1), opts)
		if _, ok := cfg.Detectors[c.wantKey]; !ok {
			t.Errorf("detector type %q: expected key %q in detectors map, got %v",
				c.detectorType, c.wantKey, cfg.Detectors)
		}
	}
}

// ─── WriteConfig tests ────────────────────────────────────────────────────────

func TestWriteConfig_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frigate.yml")

	cameras := makeCameras(3)
	cfg := frigate.Generate(cameras, frigate.DefaultOptions())

	if err := frigate.WriteConfig(cfg, path); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	// File must exist
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	// Must be valid YAML
	data, _ := os.ReadFile(path)
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("written config is not valid YAML: %v\n\nContent:\n%s", err, data)
	}

	// Must contain cameras key
	if _, ok := parsed["cameras"]; !ok {
		t.Error("yaml missing 'cameras' key")
	}
	if _, ok := parsed["mqtt"]; !ok {
		t.Error("yaml missing 'mqtt' key")
	}
}

func TestWriteConfig_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frigate.yml")

	// Write once
	cfg1 := frigate.Generate(makeCameras(1), frigate.DefaultOptions())
	frigate.WriteConfig(cfg1, path)

	// Write again (should overwrite atomically, not leave .tmp file)
	cfg2 := frigate.Generate(makeCameras(2), frigate.DefaultOptions())
	if err := frigate.WriteConfig(cfg2, path); err != nil {
		t.Fatalf("second WriteConfig: %v", err)
	}

	// No .tmp file should exist
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temp file was not cleaned up")
	}

	// File should reflect cfg2 (2 cameras)
	data, _ := os.ReadFile(path)
	var parsed struct {
		Cameras map[string]interface{} `yaml:"cameras"`
	}
	yaml.Unmarshal(data, &parsed)
	if len(parsed.Cameras) != 2 {
		t.Errorf("expected 2 cameras after overwrite, got %d", len(parsed.Cameras))
	}
}

// ─── DiffCameras tests ────────────────────────────────────────────────────────

func TestDiffCameras_NewCamera(t *testing.T) {
	prev := makeCameras(2)
	next := makeCameras(3) // 3 cameras = prev 2 + 1 new

	added, removed := frigate.DiffCameras(prev, next)

	if len(added) != 1 {
		t.Errorf("expected 1 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
}

func TestDiffCameras_RemovedCamera(t *testing.T) {
	prev := makeCameras(3)
	next := prev[:2] // remove last camera

	added, removed := frigate.DiffCameras(prev, next)

	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(removed))
	}
}

func TestDiffCameras_NoDiff(t *testing.T) {
	prev := makeCameras(4)
	next := makeCameras(4) // same IDs

	added, removed := frigate.DiffCameras(prev, next)

	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no diff, got added=%d removed=%d", len(added), len(removed))
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func makeCameras(n int) []discovery.Camera {
	cameras := make([]discovery.Camera, n)
	for i := 0; i < n; i++ {
		cameras[i] = discovery.Camera{
			ID:           fmt.Sprintf("cam-%03d", i),
			Name:         fmt.Sprintf("Camera %d", i),
			Manufacturer: "Hikvision",
			Model:        "DS-2CD2143",
			Serial:       fmt.Sprintf("SN-%03d", i),
			IP:           fmt.Sprintf("192.168.1.%d", 100+i),
			MainStream: discovery.RTSPStream{
				URL:    fmt.Sprintf("rtsp://admin:pass@192.168.1.%d/stream1", 100+i),
				Width:  1920, Height: 1080, FPS: 25,
			},
			SubStream: discovery.RTSPStream{
				URL:    fmt.Sprintf("rtsp://admin:pass@192.168.1.%d/stream2", 100+i),
				Width:  640, Height: 360, FPS: 15,
			},
		}
	}
	return cameras
}

func isAlphanumOrUnderscore(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
}

// Ensure fmt and strings are used
var _ = strings.Contains
