package translator

import (
	"math"
)

type HueStateChangeRequest struct {
	On             *bool     `json:"on,omitempty"`
	Bri            *int      `json:"bri,omitempty"`
	Hue            *int      `json:"hue,omitempty"`
	Sat            *int      `json:"sat,omitempty"`
	XY             []float64 `json:"xy,omitempty"`
	CT             *int      `json:"ct,omitempty"`
	TransitionTime *int      `json:"transitiontime,omitempty"`
	Alert          *string   `json:"alert,omitempty"`
	Effect         *string   `json:"effect,omitempty"`
}

// Translate converts Hue state change request to Z2M payload.
func Translate(req HueStateChangeRequest) map[string]interface{} {
	payload := make(map[string]interface{})

	if req.On != nil {
		if *req.On {
			payload["state"] = "ON"
		} else {
			payload["state"] = "OFF"
		}
	}

	if req.Bri != nil {
		payload["brightness"] = *req.Bri
	}

	if req.CT != nil {
		payload["color_temp"] = *req.CT
	}

	if len(req.XY) == 2 {
		payload["color"] = map[string]interface{}{
			"x": req.XY[0],
			"y": req.XY[1],
		}
	}

	if req.Hue != nil || req.Sat != nil {
		colorMap := make(map[string]interface{})
		if req.Hue != nil {
			colorMap["hue"] = int(math.Round(float64(*req.Hue) * 360.0 / 65535.0))
		}
		if req.Sat != nil {
			colorMap["saturation"] = int(math.Round(float64(*req.Sat) * 100.0 / 254.0))
		}
		payload["color"] = colorMap
	}

	if req.TransitionTime != nil {
		// transition time is in deciseconds, Z2M expects transition time in seconds (float)
		payload["transition"] = float64(*req.TransitionTime) / 10.0
	}

	return payload
}
