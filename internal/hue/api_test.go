package hue

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"hue2mqtt/internal/config"
	"hue2mqtt/internal/mqtt"
)

func TestBuildHueLight(t *testing.T) {
	// Test extended color bulb mapping
	lCfg := config.LightConfig{
		ID:           "1",
		FriendlyName: "bedroom_light_1",
		Capabilities: "extended_color",
	}
	mState := &mqtt.LightState{
		On:         true,
		Brightness: 200,
		ColorTemp:  370,
		Hue:        32000,
		Sat:        120,
		XY:         [2]float64{0.45, 0.4},
		ColorMode:  "xy",
		Reachable:  true,
	}

	light := BuildHueLight(lCfg, mState, "AA:BB:CC:DD:EE:FF")

	if light.Name != "bedroom_light_1" {
		t.Errorf("expected light name 'bedroom_light_1', got %s", light.Name)
	}
	if light.UniqueID != "00:17:88:01:04:b2:b2:01-0b" {
		t.Errorf("expected UniqueID 00:17:88:01:04:b2:b2:01-0b, got %s", light.UniqueID)
	}
	if light.State.On != true {
		t.Errorf("expected state.On true")
	}
	if *light.State.Bri != 200 {
		t.Errorf("expected bri 200, got %d", *light.State.Bri)
	}
	if *light.State.CT != 370 {
		t.Errorf("expected ct 370, got %d", *light.State.CT)
	}
	if light.State.XY[0] != 0.45 || light.State.XY[1] != 0.4 {
		t.Errorf("expected xy [0.45, 0.4], got %v", light.State.XY)
	}
	if light.State.Colormode != "xy" {
		t.Errorf("expected colormode 'xy', got %s", light.State.Colormode)
	}

	// Test plug mapping (on_off)
	plugCfg := config.LightConfig{
		ID:           "2",
		FriendlyName: "Plug 1",
		Capabilities: "on_off",
	}
	plugState := &mqtt.LightState{
		On:        true,
		Reachable: true,
	}

	plugLight := BuildHueLight(plugCfg, plugState, "AA:BB:CC:DD:EE:FF")
	if plugLight.State.Bri != nil {
		t.Errorf("expected bri to be nil for plug")
	}
	if plugLight.State.Hue != nil {
		t.Errorf("expected hue to be nil for plug")
	}
	if plugLight.State.XY != nil {
		t.Errorf("expected xy to be nil for plug")
	}
	if plugLight.State.Colormode != "" {
		t.Errorf("expected colormode to be empty for plug")
	}
}

func TestHTTPAPIHandlers(t *testing.T) {
	// Setup config file
	tempFile, err := os.CreateTemp("", "api_test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	configYaml := []byte(`
bridge:
  name: "test-bridge"
  mac: "11:22:33:44:55:66"
  http_port: 80
  link_button: true
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

	mqttClient := mqtt.NewClient(cfgMgr)
	mqttClient.SetDiscoveredLights([]config.LightConfig{
		{
			ID:           "1",
			FriendlyName: "bedroom_light_1",
			Capabilities: "extended_color",
			Name:         "bedroom_light_1",
		},
	})
	mqttClient.SetLightState("bedroom_light_1", mqtt.LightState{
		On:         true,
		Brightness: 100,
		Reachable:  true,
	})

	server := NewServer(cfgMgr, mqttClient)

	// Create test server router
	mux := http.NewServeMux()
	mux.HandleFunc("GET /description.xml", server.handleDescription)
	mux.HandleFunc("GET /api/config", server.handleConfigPublic)
	mux.HandleFunc("POST /api", server.handleRegister)
	mux.HandleFunc("GET /api/{username}/lights", server.handleLightsList)
	mux.HandleFunc("GET /api/{username}/lights/{id}", server.handleLightGet)
	mux.HandleFunc("PUT /api/{username}/lights/{id}/state", server.handleLightStatePut)

	// 1. Test GET /description.xml
	req, _ := http.NewRequest("GET", "/description.xml", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /description.xml returned status %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<friendlyName>Philips hue (") {
		t.Errorf("XML response doesn't contain friendly name: %s", body)
	}

	// 2. Test POST /api (Pairing, link_button = true)
	pairReq, _ := http.NewRequest("POST", "/api", strings.NewReader(`{"devicetype":"test#app"}`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, pairReq)

	if rr.Code != http.StatusOK {
		t.Errorf("POST /api returned status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hue2mqtt-user") {
		t.Errorf("expected success response with username, got: %s", rr.Body.String())
	}

	// 3. Test POST /api (Pairing, link_button = false)
	cfgMgr.GetConfig().Bridge.LinkButton = false
	pairReq2, _ := http.NewRequest("POST", "/api", strings.NewReader(`{"devicetype":"test#app"}`))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, pairReq2)

	if rr.Code != http.StatusOK {
		t.Errorf("POST /api returned status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "error") || !strings.Contains(rr.Body.String(), "101") {
		t.Errorf("expected error 101 link button not pressed, got: %s", rr.Body.String())
	}

	// 4. Test GET /api/hue2mqtt-user/lights
	lightsReq, _ := http.NewRequest("GET", "/api/hue2mqtt-user/lights", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, lightsReq)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /api/username/lights returned status %d", rr.Code)
	}
	var lightsList map[string]HueLight
	if err := json.Unmarshal(rr.Body.Bytes(), &lightsList); err != nil {
		t.Fatalf("failed to unmarshal lights response: %v", err)
	}
	if len(lightsList) != 1 || lightsList["1"].Name != "bedroom_light_1" {
		t.Errorf("unexpected lights response: %+v", lightsList)
	}

	// 5. Test PUT /api/hue2mqtt-user/lights/1/state
	stateChangeJSON := `{"on":false,"bri":220,"ct":450}`
	stateReq, _ := http.NewRequest("PUT", "/api/hue2mqtt-user/lights/1/state", bytes.NewBufferString(stateChangeJSON))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, stateReq)

	if rr.Code != http.StatusOK {
		t.Errorf("PUT /api/username/lights/1/state returned status %d", rr.Code)
	}

	// Verify local state was modified
	mStateNew, _ := mqttClient.GetLightState("bedroom_light_1")
	if mStateNew.On != false {
		t.Errorf("expected state On to be updated to false")
	}
	if mStateNew.Brightness != 220 {
		t.Errorf("expected state Brightness to be updated to 220")
	}
	if mStateNew.ColorTemp != 450 {
		t.Errorf("expected state ColorTemp to be updated to 450")
	}
}
