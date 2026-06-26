package config

import (
	"os"
	"testing"
)

func TestDeriveBridgeID(t *testing.T) {
	tests := []struct {
		mac  string
		want string
	}{
		{"AA:BB:CC:DD:EE:FF", "AABBCCFFFEDDEEFF"},
		{"aa:bb:cc:dd:ee:ff", "AABBCCFFFEDDEEFF"},
		{"AA-BB-CC-DD-EE-FF", "AABBCCFFFEDDEEFF"},
		{"AABBCCDDEEFF", "AABBCCFFFEDDEEFF"},
	}

	for _, tt := range tests {
		got := DeriveBridgeID(tt.mac)
		if got != tt.want {
			t.Errorf("DeriveBridgeID(%q) = %q; want %q", tt.mac, got, tt.want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	tempFile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	validYaml := []byte(`
bridge:
  name: "test-bridge"
  mac: "11:22:33:44:55:66"
  http_port: 8080
mqtt:
  server: "localhost"
  port: 1883
lights:
  - friendly_name: "Light 1"
    capabilities: "extended_color"
`)
	if _, err := tempFile.Write(validYaml); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tempFile.Close()

	mgr, err := Load(tempFile.Name())
	if err != nil {
		t.Errorf("unexpected error loading valid config: %v", err)
	}

	cfg := mgr.GetConfig()
	if cfg.Bridge.Name != "test-bridge" {
		t.Errorf("expected bridge name test-bridge, got %s", cfg.Bridge.Name)
	}
	if cfg.Bridge.HTTPPort != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Bridge.HTTPPort)
	}
	if cfg.Bridge.LogLevel != "info" {
		t.Errorf("expected default log level to be 'info', got %q", cfg.Bridge.LogLevel)
	}
	if len(cfg.Lights) != 1 || cfg.Lights[0].FriendlyName != "Light 1" {
		t.Errorf("incorrect light configuration loaded")
	}
}

func TestInvalidConfigValidation(t *testing.T) {
	tempFile, err := os.CreateTemp("", "invalid_config_test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	invalidYaml := []byte(`
bridge:
  name: "test-bridge"
  mac: "invalid-mac-format"
mqtt:
  server: "localhost"
lights:
  - friendly_name: "Light 1"
    capabilities: "unsupported_capability"
`)
	if _, err := tempFile.Write(invalidYaml); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tempFile.Close()

	_, err = Load(tempFile.Name())
	if err == nil {
		t.Errorf("expected error loading invalid config, got nil")
	}
}
