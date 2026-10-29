package tes

import "testing"

func TestGPUCount(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parameters map[string]string
		want       int
		valid      bool
	}{
		{"absent", nil, 0, true},
		{"one", map[string]string{"gpu_count": "1"}, 1, true},
		{"multiple", map[string]string{"gpu_count": "4"}, 4, true},
		{"zero", map[string]string{"gpu_count": "0"}, 0, false},
		{"negative", map[string]string{"gpu_count": "-1"}, 0, false},
		{"fraction", map[string]string{"gpu_count": "1.5"}, 0, false},
		{"empty", map[string]string{"gpu_count": ""}, 0, false},
		{"whitespace", map[string]string{"gpu_count": " 1"}, 0, false},
		{"plus sign", map[string]string{"gpu_count": "+1"}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GPUCount(&Resources{BackendParameters: tc.parameters})
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("GPUCount = %d, %v; want %d, valid=%t", got, err, tc.want, tc.valid)
			}
		})
	}
}
