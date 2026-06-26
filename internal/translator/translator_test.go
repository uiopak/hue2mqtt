package translator

import (
	"reflect"
	"testing"
)

func TestTranslate(t *testing.T) {
	trueVal := true
	falseVal := false
	briVal := 200
	ctVal := 370
	hueVal := 32767
	satVal := 127
	transitionVal := 40

	tests := []struct {
		name     string
		req      HueStateChangeRequest
		expected map[string]interface{}
	}{
		{
			name: "State ON",
			req: HueStateChangeRequest{
				On: &trueVal,
			},
			expected: map[string]interface{}{
				"state": "ON",
			},
		},
		{
			name: "State OFF",
			req: HueStateChangeRequest{
				On: &falseVal,
			},
			expected: map[string]interface{}{
				"state": "OFF",
			},
		},
		{
			name: "Brightness and Color Temperature",
			req: HueStateChangeRequest{
				Bri: &briVal,
				CT:  &ctVal,
			},
			expected: map[string]interface{}{
				"brightness": 200,
				"color_temp": 370,
			},
		},
		{
			name: "XY Color",
			req: HueStateChangeRequest{
				XY: []float64{0.45, 0.4},
			},
			expected: map[string]interface{}{
				"color": map[string]interface{}{
					"x": 0.45,
					"y": 0.4,
				},
			},
		},
		{
			name: "Hue and Saturation Color conversion",
			req: HueStateChangeRequest{
				Hue: &hueVal,
				Sat: &satVal,
			},
			expected: map[string]interface{}{
				"color": map[string]interface{}{
					"hue":        180, // 32767 * 360 / 65535 = 180
					"saturation": 50,  // 127 * 100 / 254 = 50
				},
			},
		},
		{
			name: "Transition Time conversion",
			req: HueStateChangeRequest{
				TransitionTime: &transitionVal,
			},
			expected: map[string]interface{}{
				"transition": 4.0, // 40 deciseconds = 4.0 seconds
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Translate(tt.req)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Translate() got = %v, expected %v", got, tt.expected)
			}
		})
	}
}
