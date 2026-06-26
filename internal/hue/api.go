package hue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"hue2mqtt/internal/config"
	"hue2mqtt/internal/mqtt"
	"hue2mqtt/internal/translator"
)

type Server struct {
	cfgManager *config.Manager
	mqttClient *mqtt.Client
	httpServer *http.Server
}

func NewServer(cfgManager *config.Manager, mqttClient *mqtt.Client) *Server {
	return &Server{
		cfgManager: cfgManager,
		mqttClient: mqttClient,
	}
}

type responseWriterWithStatus struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (w *responseWriterWithStatus) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriterWithStatus) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (s *Server) Start() error {
	cfg := s.cfgManager.GetConfig()
	addr := fmt.Sprintf(":%d", cfg.Bridge.HTTPPort)

	mux := http.NewServeMux()

	// Register API endpoints
	mux.HandleFunc("GET /description.xml", s.handleDescription)
	mux.HandleFunc("GET /api/config", s.handleConfigPublic)
	mux.HandleFunc("POST /api", s.handleRegister)
	mux.HandleFunc("GET /api/{username}", s.handleFullState)
	mux.HandleFunc("GET /api/{username}/lights", s.handleLightsList)
	mux.HandleFunc("GET /api/{username}/lights/{id}", s.handleLightGet)
	mux.HandleFunc("PUT /api/{username}/lights/{id}/state", s.handleLightStatePut)
	mux.HandleFunc("GET /api/{username}/config", s.handleConfigPrivate)

	// Wrap in logging middleware
	loggingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Clean trailing slash internally for routing (if path is not just "/")
		originalPath := r.URL.Path
		cleanedPath := originalPath
		if len(cleanedPath) > 1 && strings.HasSuffix(cleanedPath, "/") {
			cleanedPath = strings.TrimSuffix(cleanedPath, "/")
		}

		// Skip logging for description.xml and GET /api/hue2mqtt-user to reduce log noise
		if cleanedPath == "/description.xml" || (r.Method == "GET" && cleanedPath == "/api/hue2mqtt-user") {
			r.URL.Path = cleanedPath
			mux.ServeHTTP(w, r)
			return
		}

		// Set cleaned path for routing and dumping
		r.URL.Path = cleanedPath

		// Capture raw request dump
		reqDump, err := httputil.DumpRequest(r, true)
		var reqDumpStr string
		if err == nil {
			reqDumpStr = string(reqDump)
		} else {
			reqDumpStr = fmt.Sprintf("Failed to dump request: %v", err)
		}

		ww := &responseWriterWithStatus{ResponseWriter: w}
		mux.ServeHTTP(ww, r)

		urlLogged := r.URL.String()
		if originalPath != r.URL.Path {
			urlLogged = fmt.Sprintf("%s (cleaned from %s)", r.URL.String(), originalPath)
		}

		logLevel := strings.ToLower(s.cfgManager.GetConfig().Bridge.LogLevel)
		if logLevel == "simple" {
			statusText := http.StatusText(ww.statusCode)
			if statusText == "" {
				statusText = "Unknown Status"
			}
			slog.Info("HTTP Request",
				"remote", r.RemoteAddr,
				"method", r.Method,
				"url", urlLogged,
				"status", ww.statusCode,
				"status_text", statusText,
				"elapsed", time.Since(start),
			)
		} else if logLevel == "verbose" {
			respBodyStr := ww.body.String()

			// Try to pretty-print JSON response if it's JSON
			var prettyJSON bytes.Buffer
			if json.Indent(&prettyJSON, []byte(respBodyStr), "", "  ") == nil {
				respBodyStr = prettyJSON.String()
			}

			timestamp := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
			statusText := http.StatusText(ww.statusCode)
			if statusText == "" {
				statusText = "Unknown Status"
			}

			// Print multi-line human-readable transaction log to stdout
			fmt.Printf("\n==================== HTTP TRANSACTION ====================\n")
			fmt.Printf("Timestamp: %s\n", timestamp)
			fmt.Printf("Remote:    %s\n", r.RemoteAddr)
			fmt.Printf("Request:   %s %s\n", r.Method, urlLogged)
			fmt.Printf("Status:    %d %s (elapsed: %v)\n", ww.statusCode, statusText, time.Since(start))
			fmt.Printf("--------------------- REQUEST DUMP ---------------------\n")
			fmt.Print(reqDumpStr)
			if !strings.HasSuffix(reqDumpStr, "\n") {
				fmt.Println()
			}
			fmt.Printf("--------------------- RESPONSE BODY --------------------\n")
			fmt.Print(respBodyStr)
			if !strings.HasSuffix(respBodyStr, "\n") {
				fmt.Println()
			}
			fmt.Printf("========================================================\n\n")
		}
	})

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: loggingHandler,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind HTTP server to %s: %w", addr, err)
	}

	slog.Info("HTTP Server: Started", "addr", addr)
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP Server: Serve failed", "error", err)
		}
	}()

	return nil
}

func (s *Server) Stop() {
	if s.httpServer != nil {
		slog.Info("HTTP Server: Shutting down...")
		_ = s.httpServer.Close()
		s.httpServer = nil
	}
}

func (s *Server) handleDescription(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgManager.GetConfig()
	localIP := GetLocalIP()
	macLower := strings.ToLower(cfg.Bridge.MAC)

	w.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8" ?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <URLBase>http://%s:%d/</URLBase>
  <device>
    <deviceType>urn:schemas-upnp-org:device:Basic:1</deviceType>
    <friendlyName>%s (%s)</friendlyName>
    <manufacturer>Signify</manufacturer>
    <manufacturerURL>http://www.meethue.com</manufacturerURL>
    <modelDescription>Philips hue Personal Wireless Lighting</modelDescription>
    <modelName>Philips hue bridge 2015</modelName>
    <modelNumber>BSB002</modelNumber>
    <modelURL>http://www.meethue.com</modelURL>
    <serialNumber>%s</serialNumber>
    <UDN>uuid:2f402f80-da50-11e1-9b23-%s</UDN>
  </device>
</root>`, localIP, cfg.Bridge.HTTPPort, cfg.Bridge.Name, localIP, macLower, macLower)
}

func (s *Server) buildHueConfig(whitelist map[string]HueWhitelist) HueConfig {
	cfg := s.cfgManager.GetConfig()
	bridgeID := s.cfgManager.BridgeID()
	localIP := GetLocalIP()
	gateway := ""
	parts := strings.Split(localIP, ".")
	if len(parts) == 4 {
		gateway = strings.Join(parts[:3], ".") + ".1"
	}

	timezone := "Europe/Warsaw"
	if loc := time.Local; loc != nil && loc.String() != "Local" && loc.String() != "" {
		timezone = loc.String()
	}

	return HueConfig{
		Name:             cfg.Bridge.Name,
		BridgeID:         bridgeID,
		MAC:              cfg.Bridge.MAC,
		DatastoreVersion: "163",
		SWVersion:        "1965111030",
		APIVersion:       "1.65.0",
		FactoryNew:       false,
		ModelID:          "BSB002",
		IPAddress:        localIP,
		Gateway:          gateway,
		Netmask:          "255.255.255.0",
		UTC:              time.Now().UTC().Format("2006-01-02T15:04:05"),
		LocalTime:        time.Now().Format("2006-01-02T15:04:05"),
		TimeZone:         timezone,
		Whitelist:        whitelist,
	}
}

func (s *Server) handleConfigPublic(w http.ResponseWriter, r *http.Request) {
	resp := s.buildHueConfig(nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgManager.GetConfig()
	w.Header().Set("Content-Type", "application/json")
	if cfg.Bridge.LinkButton {
		slog.Info("Pairing request accepted (link button is enabled)")
		_, _ = w.Write([]byte(`[{"success": {"username": "hue2mqtt-user"}}]`))
	} else {
		slog.Info("Pairing request rejected (link button is disabled)")
		_, _ = w.Write([]byte(`[{"error": {"type": 101, "address": "", "description": "link button not pressed"}}]`))
	}
}

func (s *Server) handleLightsList(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}
	cfg := s.cfgManager.GetConfig()
	lights := make(map[string]HueLight)
	for _, l := range cfg.Lights {
		mstate, _ := s.mqttClient.GetLightState(l.FriendlyName)
		lights[l.ID] = BuildHueLight(l, &mstate, cfg.Bridge.MAC)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(lights)
}

func (s *Server) handleLightGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}
	id := r.PathValue("id")
	cfg := s.cfgManager.GetConfig()

	var matchedLight *config.LightConfig
	for i := range cfg.Lights {
		if cfg.Lights[i].ID == id {
			matchedLight = &cfg.Lights[i]
			break
		}
	}

	if matchedLight == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"error": {"type": 3, "address": "/lights/` + id + `", "description": "resource, ` + id + `, not available"}}]`))
		return
	}

	mstate, _ := s.mqttClient.GetLightState(matchedLight.FriendlyName)
	hueLight := BuildHueLight(*matchedLight, &mstate, cfg.Bridge.MAC)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hueLight)
}

func (s *Server) handleConfigPrivate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	whitelist := map[string]HueWhitelist{
		"hue2mqtt-user": {
			LastUseDate: time.Now().Format("2006-01-02T15:04:05"),
			CreateDate:  "2026-01-01T00:00:00",
			Name:        "hue2mqtt-user",
		},
	}

	resp := s.buildHueConfig(whitelist)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleFullState(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}
	cfg := s.cfgManager.GetConfig()

	lights := make(map[string]HueLight)
	for _, l := range cfg.Lights {
		mstate, _ := s.mqttClient.GetLightState(l.FriendlyName)
		lights[l.ID] = BuildHueLight(l, &mstate, cfg.Bridge.MAC)
	}

	whitelist := map[string]HueWhitelist{
		"hue2mqtt-user": {
			LastUseDate: time.Now().Format("2006-01-02T15:04:05"),
			CreateDate:  "2026-01-01T00:00:00",
			Name:        "hue2mqtt-user",
		},
	}

	configResp := s.buildHueConfig(whitelist)

	resp := HueFullState{
		Lights:        lights,
		Groups:        make(map[string]interface{}),
		Config:        configResp,
		Scenes:        make(map[string]interface{}),
		Schedules:     make(map[string]interface{}),
		Sensors:       make(map[string]interface{}),
		Rules:         make(map[string]interface{}),
		ResourceLinks: make(map[string]interface{}),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleLightStatePut(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}
	id := r.PathValue("id")
	cfg := s.cfgManager.GetConfig()

	var matchedLight *config.LightConfig
	for i := range cfg.Lights {
		if cfg.Lights[i].ID == id {
			matchedLight = &cfg.Lights[i]
			break
		}
	}

	if matchedLight == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"error": {"type": 3, "address": "/lights/` + id + `", "description": "resource, ` + id + `, not available"}}]`))
		return
	}

	var req translator.HueStateChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode state change request", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	slog.Debug("Received state change command for light", "id", id, "friendly_name", matchedLight.FriendlyName, "req", fmt.Sprintf("%+v", req))

	// Construct success responses
	type successItem map[string]interface{}
	type responseItem struct {
		Success successItem `json:"success"`
	}

	resp := make([]responseItem, 0)

	mstate, exists := s.mqttClient.GetLightState(matchedLight.FriendlyName)
	if !exists {
		mstate = mqtt.LightState{Reachable: true}
	}

	if req.On != nil {
		mstate.On = *req.On
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/on", id): *req.On}})
	}
	if req.Bri != nil {
		mstate.Brightness = *req.Bri
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/bri", id): *req.Bri}})
	}
	if req.CT != nil {
		mstate.ColorTemp = *req.CT
		mstate.ColorMode = "ct"
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/ct", id): *req.CT}})
	}
	if req.Hue != nil {
		mstate.Hue = *req.Hue
		mstate.ColorMode = "hs"
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/hue", id): *req.Hue}})
	}
	if req.Sat != nil {
		mstate.Sat = *req.Sat
		mstate.ColorMode = "hs"
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/sat", id): *req.Sat}})
	}
	if len(req.XY) == 2 {
		mstate.XY = [2]float64{req.XY[0], req.XY[1]}
		mstate.ColorMode = "xy"
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/xy", id): req.XY}})
	}
	if req.Alert != nil {
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/alert", id): *req.Alert}})
	}
	if req.Effect != nil {
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/effect", id): *req.Effect}})
	}
	if req.TransitionTime != nil {
		resp = append(resp, responseItem{Success: successItem{fmt.Sprintf("/lights/%s/state/transitiontime", id): *req.TransitionTime}})
	}

	// Update cached state in memory
	s.mqttClient.SetLightState(matchedLight.FriendlyName, mstate)

	// Translate and publish to Zigbee2MQTT
	z2mPayload := translator.Translate(req)
	if len(z2mPayload) > 0 {
		err := s.mqttClient.Publish(matchedLight.FriendlyName, z2mPayload)
		if err != nil {
			slog.Error("Failed to publish translated state to Zigbee2MQTT", "friendly_name", matchedLight.FriendlyName, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	username := r.PathValue("username")
	if username != "hue2mqtt-user" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"error": {"type": 1, "address": "/", "description": "unauthorized user"}}]`))
		return false
	}
	return true
}
