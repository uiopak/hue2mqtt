package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"hue2mqtt/internal/config"
)

type LightState struct {
	On         bool
	Brightness int // 0-254
	ColorTemp  int // mireds 153-500
	Hue        int // 0-65535
	Sat        int // 0-254
	XY         [2]float64
	ColorMode  string // "ct", "hs", "xy"
	Reachable  bool
}

type Z2MDevice struct {
	FriendlyName string `json:"friendly_name"`
	IEEEAddress  string `json:"ieee_address"`
	Type         string `json:"type"`
	Definition   *struct {
		Model       string `json:"model"`
		Vendor      string `json:"vendor"`
		Description string `json:"description"`
	} `json:"definition"`
	Supported bool `json:"supported"`
}

type z2mColor struct {
	X          *float64 `json:"x"`
	Y          *float64 `json:"y"`
	Hue        *int     `json:"hue"`
	Saturation *int     `json:"saturation"`
}

type z2mState struct {
	State      *string   `json:"state"`
	Brightness *int      `json:"brightness"`
	ColorTemp  *int      `json:"color_temp"`
	Color      *z2mColor `json:"color"`
	ColorMode  *string   `json:"color_mode"`
}

type Client struct {
	mu           sync.RWMutex
	cfgManager   *config.Manager
	mqttClient   mqtt.Client
	states       map[string]*LightState // keyed by friendly_name
	activeServer string                 // tcp://host:port
	subscribed   map[string]bool        // tracks subscribed topics
}

func NewClient(cfgManager *config.Manager) *Client {
	c := &Client{
		cfgManager: cfgManager,
		states:     make(map[string]*LightState),
		subscribed: make(map[string]bool),
	}

	cfgManager.RegisterCallback(c.onConfigChange)
	return c
}

func (c *Client) onConfigChange(cfg *config.Config) {
	newServer := fmt.Sprintf("tcp://%s:%d", cfg.MQTT.Server, cfg.MQTT.Port)

	c.mu.Lock()
	isDifferentServer := newServer != c.activeServer
	c.mu.Unlock()

	if isDifferentServer {
		slog.Info("MQTT broker host/port changed, reconnecting...", "new_server", newServer)
		go c.Reconnect()
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mqttClient != nil && c.mqttClient.IsConnected() {
		slog.Info("MQTT configuration updated, syncing light subscriptions...")
		c.syncSubscriptions(cfg)
	}
}

func (c *Client) Connect() error {
	c.mu.Lock()
	cfg := c.cfgManager.GetConfig()
	serverURI := fmt.Sprintf("tcp://%s:%d", cfg.MQTT.Server, cfg.MQTT.Port)
	c.activeServer = serverURI
	c.mu.Unlock()

	opts := mqtt.NewClientOptions()
	opts.AddBroker(serverURI)
	if cfg.MQTT.Username != "" {
		opts.SetUsername(cfg.MQTT.Username)
	}
	if cfg.MQTT.Password != "" {
		opts.SetPassword(cfg.MQTT.Password)
	}
	opts.SetClientID(fmt.Sprintf("%s-%d", cfg.Bridge.Name, time.Now().UnixNano()))
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		slog.Warn("MQTT connection lost", "error", err)
	})

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("MQTT connection failed: %w", token.Error())
	}

	c.mu.Lock()
	c.mqttClient = client
	c.mu.Unlock()

	slog.Info("Successfully connected to MQTT broker", "server", serverURI)
	return nil
}

func (c *Client) Reconnect() {
	c.mu.Lock()
	client := c.mqttClient
	c.mu.Unlock()

	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}

	for {
		err := c.Connect()
		if err == nil {
			break
		}
		slog.Error("Failed to connect to MQTT broker, retrying in 5 seconds", "error", err)
		time.Sleep(5 * time.Second)
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mqttClient != nil && c.mqttClient.IsConnected() {
		c.mqttClient.Disconnect(250)
		slog.Info("MQTT client disconnected")
	}
}

func (c *Client) onConnect(client mqtt.Client) {
	slog.Info("MQTT connection established")

	c.mu.Lock()
	defer c.mu.Unlock()

	cfg := c.cfgManager.GetConfig()
	c.subscribed = make(map[string]bool)

	// Subscribe to device announcements
	devicesTopic := "zigbee2mqtt/bridge/devices"
	token := client.Subscribe(devicesTopic, 0, c.handleBridgeDevices)
	token.Wait()
	c.subscribed[devicesTopic] = true

	// Subscribe to state topics for each configured light
	for _, l := range cfg.Lights {
		topic := fmt.Sprintf("zigbee2mqtt/%s", l.FriendlyName)
		token := client.Subscribe(topic, 0, c.handleLightState)
		token.Wait()
		c.subscribed[topic] = true

		if _, exists := c.states[l.FriendlyName]; !exists {
			c.states[l.FriendlyName] = &LightState{Reachable: false}
		}
	}

	// Request list of devices
	getDevicesTopic := "zigbee2mqtt/bridge/config/devices/get"
	slog.Debug("Requesting device list from Zigbee2MQTT", "topic", getDevicesTopic)
	client.Publish(getDevicesTopic, 0, false, "")
}

func (c *Client) syncSubscriptions(cfg *config.Config) {
	if c.mqttClient == nil || !c.mqttClient.IsConnected() {
		return
	}

	desired := make(map[string]bool)
	desired["zigbee2mqtt/bridge/devices"] = true
	for _, l := range cfg.Lights {
		topic := fmt.Sprintf("zigbee2mqtt/%s", l.FriendlyName)
		desired[topic] = true
	}

	// Unsubscribe from topics no longer in config
	for topic := range c.subscribed {
		if !desired[topic] {
			slog.Debug("Unsubscribing from MQTT topic", "topic", topic)
			c.mqttClient.Unsubscribe(topic)
			delete(c.subscribed, topic)
		}
	}

	// Subscribe to new topics
	for topic := range desired {
		if !c.subscribed[topic] {
			slog.Debug("Subscribing to MQTT topic", "topic", topic)
			var token mqtt.Token
			if topic == "zigbee2mqtt/bridge/devices" {
				token = c.mqttClient.Subscribe(topic, 0, c.handleBridgeDevices)
			} else {
				token = c.mqttClient.Subscribe(topic, 0, c.handleLightState)
			}
			token.Wait()
			c.subscribed[topic] = true
		}
	}

	// Clean up states map
	activeFriendlyNames := make(map[string]bool)
	for _, l := range cfg.Lights {
		activeFriendlyNames[l.FriendlyName] = true
	}
	for name := range c.states {
		if !activeFriendlyNames[name] {
			delete(c.states, name)
		}
	}
}

func (c *Client) handleBridgeDevices(client mqtt.Client, msg mqtt.Message) {
	slog.Debug("Received bridge devices list", "topic", msg.Topic())
	var devices []Z2MDevice
	if err := json.Unmarshal(msg.Payload(), &devices); err != nil {
		slog.Error("Failed to parse bridge/devices JSON", "error", err)
		return
	}

	deviceMap := make(map[string]Z2MDevice)
	for _, d := range devices {
		deviceMap[d.FriendlyName] = d
	}

	cfg := c.cfgManager.GetConfig()
	for _, l := range cfg.Lights {
		if d, found := deviceMap[l.FriendlyName]; found {
			model := "Unknown"
			vendor := "Unknown"
			desc := "No description"
			if d.Definition != nil {
				if d.Definition.Model != "" {
					model = d.Definition.Model
				}
				if d.Definition.Vendor != "" {
					vendor = d.Definition.Vendor
				}
				if d.Definition.Description != "" {
					desc = d.Definition.Description
				}
			}
			slog.Info("Light found in Zigbee2MQTT",
				"friendly_name", l.FriendlyName,
				"model", model,
				"vendor", vendor,
				"description", desc,
				"ieee_address", d.IEEEAddress,
				"supported", d.Supported,
			)
			c.mu.Lock()
			if state, exists := c.states[l.FriendlyName]; exists {
				state.Reachable = true
			}
			c.mu.Unlock()
		} else {
			slog.Warn("Light NOT found in Zigbee2MQTT — check configuration",
				"friendly_name", l.FriendlyName,
			)
		}
	}
}

func (c *Client) handleLightState(client mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	friendlyName := strings.TrimPrefix(topic, "zigbee2mqtt/")

	slog.Debug("Received light state raw payload", "friendly_name", friendlyName, "payload", string(msg.Payload()))

	var update z2mState
	if err := json.Unmarshal(msg.Payload(), &update); err != nil {
		slog.Error("Failed to parse light state JSON", "friendly_name", friendlyName, "error", err)
		return
	}

	c.mu.Lock()
	state, exists := c.states[friendlyName]
	if !exists {
		state = &LightState{}
		c.states[friendlyName] = state
	}

	state.Reachable = true
	stateChanged := false

	if update.State != nil {
		on := *update.State == "ON"
		if state.On != on {
			state.On = on
			stateChanged = true
		}
	}

	if update.Brightness != nil {
		bri := *update.Brightness
		if state.Brightness != bri {
			state.Brightness = bri
			stateChanged = true
		}
	}

	if update.ColorTemp != nil {
		ct := *update.ColorTemp
		if state.ColorTemp != ct {
			state.ColorTemp = ct
			stateChanged = true
		}
	}

	if update.ColorMode != nil {
		mode := *update.ColorMode
		hueMode := mode
		if mode == "color_temp" {
			hueMode = "ct"
		}
		if state.ColorMode != hueMode {
			state.ColorMode = hueMode
			stateChanged = true
		}
	}

	if update.Color != nil {
		col := update.Color
		if col.X != nil && col.Y != nil {
			if state.XY[0] != *col.X || state.XY[1] != *col.Y {
				state.XY = [2]float64{*col.X, *col.Y}
				stateChanged = true
			}
		}
		if col.Hue != nil {
			// Convert Z2M Hue (0-360) to Hue API Hue (0-65535)
			hue := int(float64(*col.Hue) * 65535.0 / 360.0)
			if state.Hue != hue {
				state.Hue = hue
				stateChanged = true
			}
		}
		if col.Saturation != nil {
			// Convert Z2M Saturation (0-100) to Hue API Saturation (0-254)
			sat := int(float64(*col.Saturation) * 254.0 / 100.0)
			if state.Sat != sat {
				state.Sat = sat
				stateChanged = true
			}
		}
	}

	on := state.On
	brightness := state.Brightness
	colorTemp := state.ColorTemp
	hue := state.Hue
	sat := state.Sat
	xy := state.XY
	colorMode := state.ColorMode
	c.mu.Unlock()

	if stateChanged {
		slog.Debug("Light state updated",
			"friendly_name", friendlyName,
			"on", on,
			"brightness", brightness,
			"color_temp", colorTemp,
			"hue", hue,
			"sat", sat,
			"xy", xy,
			"color_mode", colorMode,
		)
	}
}

func (c *Client) GetLightState(friendlyName string) (LightState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state, exists := c.states[friendlyName]
	if !exists {
		return LightState{}, false
	}
	return *state, true
}

func (c *Client) SetLightState(friendlyName string, state LightState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[friendlyName] = &state
}

func (c *Client) Publish(friendlyName string, payload map[string]interface{}) error {
	c.mu.RLock()
	client := c.mqttClient
	c.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return fmt.Errorf("MQTT client not connected")
	}

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", friendlyName)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal publish payload: %w", err)
	}

	slog.Debug("Publishing to MQTT", "topic", topic, "payload", string(data))
	token := client.Publish(topic, 0, false, data)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish: %w", token.Error())
	}
	return nil
}
