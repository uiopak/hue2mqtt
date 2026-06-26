package main

import (
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"hue2mqtt/internal/config"
	"hue2mqtt/internal/hue"
	"hue2mqtt/internal/mqtt"
)

func main() {
	// Configure structured logging with a dynamic level
	programLevel := new(slog.LevelVar)
	programLevel.Set(slog.LevelInfo)
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: programLevel,
	})
	slog.SetDefault(slog.New(handler))

	slog.Info("Starting hue2mqtt service...")

	// Load configuration
	cfgManager, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Set dynamic log level based on configuration
	setLogLevel := func(levelStr string) {
		switch strings.ToLower(levelStr) {
		case "verbose", "simple":
			programLevel.Set(slog.LevelDebug)
		default: // "info"
			programLevel.Set(slog.LevelInfo)
		}
	}
	setLogLevel(cfgManager.GetConfig().Bridge.LogLevel)

	// Update log level dynamically on config changes
	cfgManager.RegisterCallback(func(newCfg *config.Config) {
		setLogLevel(newCfg.Bridge.LogLevel)
	})

	// Start configuration file watcher for hot-reloads
	go cfgManager.WatchForChanges()

	// Initialize and connect MQTT client
	mqttClient := mqtt.NewClient(cfgManager)
	err = mqttClient.Connect()
	if err != nil {
		slog.Error("Failed to initialize MQTT client", "error", err)
		os.Exit(1)
	}
	defer mqttClient.Close()

	// Start SSDP and mDNS discovery advertising
	discoveryMgr := hue.NewDiscoveryManager(cfgManager)
	if err := discoveryMgr.Start(); err != nil {
		slog.Error("Failed to start discovery manager", "error", err)
		os.Exit(1)
	}
	defer discoveryMgr.Stop()

	// Start the Hue Bridge API HTTP server
	apiServer := hue.NewServer(cfgManager, mqttClient)
	if err := apiServer.Start(); err != nil {
		slog.Error("Failed to start HTTP server", "error", err)
		os.Exit(1)
	}
	defer apiServer.Stop()

	slog.Info("hue2mqtt Phase 2 initialized. Press Ctrl+C to exit.")

	// Block until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down hue2mqtt service...")
}
