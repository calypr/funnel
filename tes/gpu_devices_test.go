package tes

import (
	"reflect"
	"testing"
)

func TestGPUDevices(t *testing.T) {
	validUUID := "GPU-0123456789abcdef0123456789abcdef"
	validMIG := "MIG-GPU-0123456789abcdef0123456789abcdef/1/0"
	validMIGUUID := "MIG-0123456789abcdef0123456789abcdef"
	tests := []struct {
		name          string
		parameters    map[string]string
		expectedCount int
		want          []string
		wantErr       bool
	}{
		{name: "absent"},
		{name: "indexes", parameters: map[string]string{"gpu_devices": "0,2"}, expectedCount: 2, want: []string{"0", "2"}},
		{name: "UUID and MIG IDs", parameters: map[string]string{"GPU_DEVICES": validUUID + "," + validMIG}, expectedCount: 2, want: []string{validUUID, validMIG}},
		{name: "MIG UUID", parameters: map[string]string{"gpu_devices": validMIGUUID}, expectedCount: 1, want: []string{validMIGUUID}},
		{name: "expected count", parameters: map[string]string{"gpu_devices": "0"}, expectedCount: 2, wantErr: true},
		{name: "empty value", parameters: map[string]string{"gpu_devices": ""}, wantErr: true},
		{name: "empty entry", parameters: map[string]string{"gpu_devices": "0,"}, wantErr: true},
		{name: "whitespace", parameters: map[string]string{"gpu_devices": "0, 1"}, wantErr: true},
		{name: "duplicate index aliases", parameters: map[string]string{"gpu_devices": "0,00"}, wantErr: true},
		{name: "duplicate UUID case", parameters: map[string]string{"gpu_devices": validUUID + "," + "gpu-0123456789ABCDEF0123456789ABCDEF"}, wantErr: true},
		{name: "all is not an ID", parameters: map[string]string{"gpu_devices": "all"}, wantErr: true},
		{name: "invalid UUID", parameters: map[string]string{"gpu_devices": "GPU-xyz"}, wantErr: true},
		{name: "negative expected count", parameters: map[string]string{"gpu_devices": "0"}, expectedCount: -1, wantErr: true},
		{name: "case collision", parameters: map[string]string{"gpu_devices": "0", "GPU_DEVICES": "1"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GPUDevices(&Resources{BackendParameters: tt.parameters}, tt.expectedCount)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GPUDevices() error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GPUDevices() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseGPUDevices(t *testing.T) {
	if _, err := ParseGPUDevices("1,2", 1); err == nil {
		t.Fatal("ParseGPUDevices() accepted a count mismatch")
	}
	got, err := ParseGPUDevices("1,2", 0)
	if err != nil || !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("ParseGPUDevices() = %#v, %v", got, err)
	}
}
