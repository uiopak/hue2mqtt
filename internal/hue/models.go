package hue

import (
	"fmt"

	"hue2mqtt/internal/config"
	"hue2mqtt/internal/mqtt"
)

type HueLightState struct {
	On        bool      `json:"on"`
	Bri       *int      `json:"bri,omitempty"`
	Hue       *int      `json:"hue,omitempty"`
	Sat       *int      `json:"sat,omitempty"`
	XY        []float64 `json:"xy,omitempty"`
	CT        *int      `json:"ct,omitempty"`
	Alert     string    `json:"alert"`
	Effect    string    `json:"effect,omitempty"`
	Colormode string    `json:"colormode,omitempty"`
	Reachable bool      `json:"reachable"`
	Mode      string    `json:"mode,omitempty"`
}

type HueLight struct {
	State            HueLightState   `json:"state"`
	Type             string          `json:"type"`
	Name             string          `json:"name"`
	ModelID          string          `json:"modelid"`
	ManufacturerName string          `json:"manufacturername"`
	ProductID        string          `json:"productid,omitempty"`
	UniqueID         string          `json:"uniqueid"`
	SWVersion        string          `json:"swversion"`
	SWConfigID       string          `json:"swconfigid,omitempty"`
	SWUpdate         HueSWUpdate     `json:"swupdate"`
	Capabilities     HueCapabilities `json:"capabilities"`
	Config           HueLightConfig  `json:"config"`
}

type HueSWUpdate struct {
	State       string `json:"state"`
	LastInstall string `json:"lastinstall"`
}

type HueCapabilities struct {
	Certified bool             `json:"certified"`
	Control   HueControlCaps   `json:"control"`
	Streaming HueStreamingCaps `json:"streaming"`
}

type HueControlCaps struct {
	MinDimLevel    int         `json:"mindimlevel,omitempty"`
	MaxLumen       int         `json:"maxlumen,omitempty"`
	ColorGamutType string      `json:"colorgamuttype,omitempty"`
	ColorGamut     [][]float64 `json:"colorgamut,omitempty"`
	CT             *HueCTCaps  `json:"ct,omitempty"`
}

type HueCTCaps struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type HueStreamingCaps struct {
	Renderer bool `json:"renderer"`
	Proxy    bool `json:"proxy"`
}

type HueLightConfig struct {
	Archetype string      `json:"archetype"`
	Function  string      `json:"function"`
	Direction string      `json:"direction"`
	Startup   *HueStartup `json:"startup,omitempty"`
}

type HueStartup struct {
	Mode       string `json:"mode"`
	Configured bool   `json:"configured"`
}

type HueConfig struct {
	Name             string                 `json:"name"`
	BridgeID         string                 `json:"bridgeid"`
	MAC              string                 `json:"mac"`
	DatastoreVersion string                 `json:"datastoreversion"`
	SWVersion        string                 `json:"swversion"`
	APIVersion       string                 `json:"apiversion"`
	FactoryNew       bool                   `json:"factorynew"`
	ReplacesBridgeID *string                `json:"replacesbridgeid"`
	ModelID          string                 `json:"modelid"`
	StarterKitID     string                 `json:"starterkitid"`
	IPAddress        string                 `json:"ipaddress,omitempty"`
	Gateway          string                 `json:"gateway,omitempty"`
	Netmask          string                 `json:"netmask,omitempty"`
	ProxyAddress     string                 `json:"proxyaddress,omitempty"`
	ProxyPort        int                    `json:"proxyport,omitempty"`
	UTC              string                 `json:"utc,omitempty"`
	LocalTime        string                 `json:"localtime,omitempty"`
	TimeZone         string                 `json:"timezone,omitempty"`
	Whitelist        map[string]HueWhitelist `json:"whitelist,omitempty"`
}

type HueWhitelist struct {
	LastUseDate string `json:"last use date"`
	CreateDate  string `json:"create date"`
	Name        string `json:"name"`
}

type HueFullState struct {
	Lights        map[string]HueLight    `json:"lights"`
	Groups        map[string]interface{} `json:"groups"`
	Config        HueConfig              `json:"config"`
	Scenes        map[string]interface{} `json:"scenes"`
	Schedules     map[string]interface{} `json:"schedules"`
	Sensors       map[string]interface{} `json:"sensors"`
	Rules         map[string]interface{} `json:"rules"`
	ResourceLinks map[string]interface{} `json:"resourcelinks"`
}

// BuildHueLight maps a configured light and its MQTT state cache to the Philips Hue API light model structure.
func BuildHueLight(lightCfg config.LightConfig, state *mqtt.LightState, bridgeMAC string) HueLight {
	uniqueID := fmt.Sprintf("00:17:88:01:04:b2:b2:%02s-0b", lightCfg.ID)

	var modelID string
	var lightType string
	var manufacturerName = "Signify Netherlands B.V."
	var productID string
	var swVersion = "1.104.2"
	var swConfigID string

	// Capabilities details
	var certified = true
	var minDimLevel = 0
	var maxLumen = 800
	var colorGamutType string
	var colorGamut [][]float64
	var ctCaps *HueCTCaps

	// Config details
	var archetype string
	var function string
	var direction = "omnidirectional"

	switch lightCfg.Capabilities {
	case "on_off":
		modelID = "LOM001"
		lightType = "On/Off plug-in unit"
		productID = "SmartPlug_OnOff_v01-00_01"
		swConfigID = "A641B5AB"
		archetype = "plug"
		function = "functional"
	case "dimmable":
		modelID = "LWB010"
		lightType = "Dimmable light"
		productID = "Philips-LWB010-1-A19DLv4"
		swVersion = "1.50.2_r30933"
		minDimLevel = 5000
		maxLumen = 806
		archetype = "classicbulb"
		function = "mixed"
	case "color_temperature":
		modelID = "LTW001"
		lightType = "Color temperature light"
		minDimLevel = 1000
		maxLumen = 806
		ctCaps = &HueCTCaps{Min: 153, Max: 454}
		archetype = "classicbulb"
		function = "functional"
	case "color":
		modelID = "LLC010"
		lightType = "Color light"
		minDimLevel = 5000
		maxLumen = 600
		colorGamutType = "A"
		colorGamut = [][]float64{{0.704, 0.296}, {0.215, 0.711}, {0.138, 0.08}}
		archetype = "hueiris"
		function = "functional"
	case "extended_color":
		fallthrough
	default:
		modelID = "LCT015"
		lightType = "Extended color light"
		productID = "Philips-LCT015-1-A19ECLv5"
		swConfigID = "772B0E5E"
		minDimLevel = 1000
		maxLumen = 800
		colorGamutType = "C"
		colorGamut = [][]float64{{0.6915, 0.3083}, {0.17, 0.7}, {0.1532, 0.0475}}
		ctCaps = &HueCTCaps{Min: 153, Max: 500}
		archetype = "sultanbulb"
		function = "mixed"
	}

	// Map State
	on := false
	reachable := false
	var briVal, hueVal, satVal, ctVal int
	var xyVal []float64
	var colormode string
	var effect string
	var mode string

	if state != nil {
		on = state.On
		reachable = state.Reachable

		// Fill fields depending on capability
		if lightCfg.Capabilities != "on_off" {
			briVal = state.Brightness
			if briVal == 0 {
				briVal = 254
			}
		}
		if lightCfg.Capabilities == "color_temperature" || lightCfg.Capabilities == "extended_color" {
			ctVal = state.ColorTemp
			if ctVal == 0 {
				ctVal = 370 // fallback default
			}
		}
		if lightCfg.Capabilities == "color" || lightCfg.Capabilities == "extended_color" {
			hueVal = state.Hue
			satVal = state.Sat
			if state.XY[0] != 0 || state.XY[1] != 0 {
				xyVal = []float64{state.XY[0], state.XY[1]}
			} else {
				xyVal = []float64{0.3804, 0.3768}
			}
			effect = "none"
		}
		if lightCfg.Capabilities == "color_temperature" || lightCfg.Capabilities == "color" || lightCfg.Capabilities == "extended_color" {
			colormode = state.ColorMode
			if colormode == "" {
				if lightCfg.Capabilities == "color_temperature" {
					colormode = "ct"
				} else {
					colormode = "xy"
				}
			}
		}
		if lightCfg.Capabilities != "on_off" {
			mode = "homeautomation"
		}
	}

	alert := "none"
	if lightCfg.Capabilities == "on_off" {
		alert = "select" // smart plug style
	}

	hueState := HueLightState{
		On:        on,
		Alert:     alert,
		Reachable: reachable,
	}

	if lightCfg.Capabilities != "on_off" {
		hueState.Bri = &briVal
		hueState.Mode = mode
	}
	if lightCfg.Capabilities == "color_temperature" || lightCfg.Capabilities == "extended_color" {
		hueState.CT = &ctVal
	}
	if lightCfg.Capabilities == "color" || lightCfg.Capabilities == "extended_color" {
		hueState.Hue = &hueVal
		hueState.Sat = &satVal
		hueState.XY = xyVal
		hueState.Effect = effect
	}
	if lightCfg.Capabilities == "color_temperature" || lightCfg.Capabilities == "color" || lightCfg.Capabilities == "extended_color" {
		hueState.Colormode = colormode
	}

	return HueLight{
		State:            hueState,
		Type:             lightType,
		Name:             lightCfg.FriendlyName,
		ModelID:          modelID,
		ManufacturerName: manufacturerName,
		ProductID:        productID,
		UniqueID:         uniqueID,
		SWVersion:        swVersion,
		SWConfigID:       swConfigID,
		SWUpdate: HueSWUpdate{
			State:       "noupdates",
			LastInstall: "2020-12-09T19:13:52",
		},
		Capabilities: HueCapabilities{
			Certified: certified,
			Control: HueControlCaps{
				MinDimLevel:    minDimLevel,
				MaxLumen:       maxLumen,
				ColorGamutType: colorGamutType,
				ColorGamut:     colorGamut,
				CT:             ctCaps,
			},
			Streaming: HueStreamingCaps{
				Renderer: lightCfg.Capabilities == "extended_color",
				Proxy:    lightCfg.Capabilities == "extended_color",
			},
		},
		Config: HueLightConfig{
			Archetype: archetype,
			Function:  function,
			Direction: direction,
			Startup: &HueStartup{
				Mode:       "safety",
				Configured: true,
			},
		},
	}
}
