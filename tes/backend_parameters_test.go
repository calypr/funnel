package tes

import (
	"reflect"
	"testing"
)

func TestBackendParameters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		params  map[string]string
		strict  bool
		want    map[string]string
		ignored []string
		fail    bool
	}{
		{name: "case insensitive", params: map[string]string{"GPU_COUNT": "2"}, want: map[string]string{"gpu_count": "2"}},
		{name: "optional unknown removed", params: map[string]string{"gpu_count": "2", "unknown": "x"}, want: map[string]string{"gpu_count": "2"}, ignored: []string{"unknown"}},
		{name: "strict unknown rejected", params: map[string]string{"unknown": "x"}, strict: true, fail: true},
		{name: "ambiguous keys", params: map[string]string{"gpu_count": "1", "GPU_COUNT": "2"}, fail: true},
		{name: "empty key", params: map[string]string{"": "x"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := &Resources{BackendParameters: tc.params, BackendParametersStrict: tc.strict}
			ignored, err := FilterBackendParameters(resources, []string{"gpu_count"})
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if tc.fail {
				return
			}
			if !reflect.DeepEqual(resources.BackendParameters, tc.want) || !reflect.DeepEqual(ignored, tc.ignored) {
				t.Fatalf("parameters=%v ignored=%v", resources.BackendParameters, ignored)
			}
		})
	}
}
