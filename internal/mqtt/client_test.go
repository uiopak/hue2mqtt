package mqtt

import (
	"os"
	"testing"

	"hue2mqtt/internal/config"
)

type mockMessage struct {
	topic   string
	payload []byte
}

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return m.topic }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

func TestHandleLightState(t *testing.T) {
	tempFile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	configYaml := []byte(`
bridge:
  name: "test-bridge"
  mac: "11:22:33:44:55:66"
mqtt:
  server: "localhost"
lights:
  - friendly_name: "bedroom_light_1"
    capabilities: "extended_color"
`)
	if _, err := tempFile.Write(configYaml); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	tempFile.Close()

	cfgMgr, err := config.Load(tempFile.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	client := NewClient(cfgMgr)

	// Set initial state
	client.states["bedroom_light_1"] = &LightState{
		On:         false,
		Brightness: 0,
		Reachable:  false,
	}

	// Mock message with color_temp state
	msgPayload := `{"state":"ON","brightness":150,"color_temp":370,"color_mode":"color_temp"}`
	msg := &mockMessage{
		topic:   "zigbee2mqtt/bedroom_light_1",
		payload: []byte(msgPayload),
	}

	client.handleLightState(nil, msg)

	state, exists := client.GetLightState("bedroom_light_1")
	if !exists {
		t.Fatalf("expected state for bedroom_light_1 to exist")
	}

	if !state.On {
		t.Errorf("expected light to be ON")
	}
	if state.Brightness != 150 {
		t.Errorf("expected brightness 150, got %d", state.Brightness)
	}
	if state.ColorTemp != 370 {
		t.Errorf("expected color_temp 370, got %d", state.ColorTemp)
	}
	if state.ColorMode != "ct" {
		t.Errorf("expected colormode 'ct', got %q", state.ColorMode)
	}
	if !state.Reachable {
		t.Errorf("expected light to be reachable")
	}

	// Mock message with xy and hs color state
	msgPayload2 := `{"state":"ON","brightness":254,"color":{"x":0.45,"y":0.4,"hue":180,"saturation":50},"color_mode":"xy"}`
	msg2 := &mockMessage{
		topic:   "zigbee2mqtt/bedroom_light_1",
		payload: []byte(msgPayload2),
	}

	client.handleLightState(nil, msg2)

	state, _ = client.GetLightState("bedroom_light_1")
	if state.Brightness != 254 {
		t.Errorf("expected brightness 254, got %d", state.Brightness)
	}
	if state.XY[0] != 0.45 || state.XY[1] != 0.4 {
		t.Errorf("expected XY [0.45, 0.4], got %v", state.XY)
	}
	// Hue: 180 out of 360 -> 32767 out of 65535 (approx 180 * 65535 / 360 = 32767)
	expectedHue := int(180 * 65535 / 360)
	if state.Hue != expectedHue {
		t.Errorf("expected Hue %d, got %d", expectedHue, state.Hue)
	}
	// Saturation: 50 out of 100 -> 127 out of 254 (50 * 254 / 100 = 127)
	expectedSat := int(50 * 254 / 100)
	if state.Sat != expectedSat {
		t.Errorf("expected Saturation %d, got %d", expectedSat, state.Sat)
	}
	if state.ColorMode != "xy" {
		t.Errorf("expected colormode 'xy', got %q", state.ColorMode)
	}
}

func TestHandleBridgeDevices(t *testing.T) {
	tempFile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	configYaml := []byte(`
bridge:
  name: "test-bridge"
  mac: "11:22:33:44:55:66"
mqtt:
  server: "localhost"
lights:
  - friendly_name: "bedroom_light_1"
    capabilities: "extended_color"
  - friendly_name: "Missing Bulb"
    capabilities: "dimmable"
`)
	if _, err := tempFile.Write(configYaml); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	tempFile.Close()

	cfgMgr, err := config.Load(tempFile.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	client := NewClient(cfgMgr)
	client.states["bedroom_light_1"] = &LightState{Reachable: false}
	client.states["Missing Bulb"] = &LightState{Reachable: false}

	devicesPayload := `[
		{
			"friendly_name": "Coordinator",
			"ieee_address": "0x00124b00258d11aa",
			"type": "Coordinator"
		},
		{
			"friendly_name": "bedroom_light_1",
			"ieee_address": "0x0017880104b2b2b2",
			"type": "Router",
			"definition": {
				"model": "LCT015",
				"vendor": "Signify Netherlands B.V.",
				"description": "Hue white and color ambiance E27"
			},
			"supported": true
		}
	]`
	msg := &mockMessage{
		topic:   "zigbee2mqtt/bridge/devices",
		payload: []byte(devicesPayload),
	}

	client.handleBridgeDevices(nil, msg)

	state1, _ := client.GetLightState("bedroom_light_1")
	if !state1.Reachable {
		t.Errorf("expected bedroom_light_1 to be marked reachable after device discovery")
	}

	state2, _ := client.GetLightState("Missing Bulb")
	if state2.Reachable {
		t.Errorf("expected Missing Bulb to remain unreachable")
	}
}
