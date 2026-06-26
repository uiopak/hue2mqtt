package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Bridge BridgeConfig  `yaml:"bridge"`
	MQTT   MQTTConfig    `yaml:"mqtt"`
	Lights []LightConfig `yaml:"lights"`
}

type BridgeConfig struct {
	Name       string `yaml:"name"`
	MAC        string `yaml:"mac"`
	HTTPPort   int    `yaml:"http_port"`
	LinkButton bool   `yaml:"link_button"`
	LogLevel   string `yaml:"log_level"`
}

type MQTTConfig struct {
	Server   string `yaml:"server"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type LightConfig struct {
	ID           string `yaml:"-"`
	FriendlyName string `yaml:"friendly_name"`
	Capabilities string `yaml:"capabilities"`
}

type Manager struct {
	mu        sync.RWMutex
	config    *Config
	filePath  string
	callbacks []func(*Config)
}

// Load loads and validates the configuration file.
func Load(filePath string) (*Manager, error) {
	m := &Manager{
		filePath: filePath,
	}
	cfg, err := m.readAndValidate()
	if err != nil {
		return nil, err
	}
	m.config = cfg
	return m, nil
}

// GetConfig returns the current active configuration thread-safely.
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// RegisterCallback registers a function to be called when the configuration is successfully reloaded.
func (m *Manager) RegisterCallback(cb func(*Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// BridgeID derives the Hue Bridge ID from the MAC address.
func (m *Manager) BridgeID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return DeriveBridgeID(m.config.Bridge.MAC)
}

// DeriveBridgeID converts a MAC address like "AA:BB:CC:DD:EE:FF" to Hue Bridge ID "AABBCCFFFEDDEEFF"
func DeriveBridgeID(mac string) string {
	cleanMAC := strings.ReplaceAll(mac, ":", "")
	cleanMAC = strings.ReplaceAll(cleanMAC, "-", "")
	cleanMAC = strings.ToUpper(cleanMAC)
	if len(cleanMAC) != 12 {
		return cleanMAC
	}
	return cleanMAC[:6] + "FFFE" + cleanMAC[6:]
}

func (m *Manager) readAndValidate() (*Config, error) {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	// Validate Bridge
	if cfg.Bridge.MAC == "" {
		return nil, fmt.Errorf("bridge MAC is required")
	}
	cleanMAC := strings.ReplaceAll(cfg.Bridge.MAC, ":", "")
	cleanMAC = strings.ReplaceAll(cleanMAC, "-", "")
	if len(cleanMAC) != 12 {
		return nil, fmt.Errorf("invalid bridge MAC: %s (must be 12-char hex after stripping separators)", cfg.Bridge.MAC)
	}

	if cfg.Bridge.HTTPPort <= 0 {
		cfg.Bridge.HTTPPort = 80 // default
	}

	if cfg.Bridge.LogLevel == "" {
		cfg.Bridge.LogLevel = "info"
	}
	cfg.Bridge.LogLevel = strings.ToLower(cfg.Bridge.LogLevel)
	switch cfg.Bridge.LogLevel {
	case "info", "simple", "verbose":
		// valid
	default:
		return nil, fmt.Errorf("invalid log_level: %s (must be one of: info, simple, verbose)", cfg.Bridge.LogLevel)
	}

	// Validate MQTT
	if cfg.MQTT.Server == "" {
		return nil, fmt.Errorf("mqtt server is required")
	}
	if cfg.MQTT.Port <= 0 {
		cfg.MQTT.Port = 1883 // default
	}

	// Validate Lights
	friendlyNames := make(map[string]bool)
	for i := range cfg.Lights {
		cfg.Lights[i].ID = strconv.Itoa(i + 1)
		l := &cfg.Lights[i]

		if l.FriendlyName == "" {
			return nil, fmt.Errorf("light at index %d is missing friendly_name", i)
		}
		if friendlyNames[l.FriendlyName] {
			return nil, fmt.Errorf("duplicate light friendly_name: %s", l.FriendlyName)
		}
		friendlyNames[l.FriendlyName] = true

		switch l.Capabilities {
		case "on_off", "dimmable", "color_temperature", "color", "extended_color":
			// valid
		default:
			return nil, fmt.Errorf("light %s has invalid capability: %s", l.FriendlyName, l.Capabilities)
		}
	}

	return &cfg, nil
}

// WatchForChanges watches the configuration directory for updates and reloads the config.
func (m *Manager) WatchForChanges() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create fsnotify watcher", "error", err)
		return
	}
	defer watcher.Close()

	absPath, err := filepath.Abs(m.filePath)
	if err != nil {
		slog.Error("Failed to get absolute path for config file", "error", err)
		return
	}
	dir := filepath.Dir(absPath)

	err = watcher.Add(dir)
	if err != nil {
		slog.Error("Failed to watch config directory", "dir", dir, "error", err)
		return
	}

	slog.Info("Watching config directory for changes", "dir", dir, "file", filepath.Base(absPath))

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			eventAbsPath, err := filepath.Abs(event.Name)
			if err != nil {
				continue
			}

			if eventAbsPath == absPath {
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					slog.Info("Config file modification detected, reloading...")
					newCfg, err := m.readAndValidate()
					if err != nil {
						slog.Error("Failed to reload configuration (validation error)", "error", err)
						continue
					}

					m.mu.Lock()
					m.config = newCfg
					callbacks := make([]func(*Config), len(m.callbacks))
					copy(callbacks, m.callbacks)
					m.mu.Unlock()

					slog.Info("Configuration reloaded successfully")
					for _, cb := range callbacks {
						go cb(newCfg)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("fsnotify watcher error", "error", err)
		}
	}
}
