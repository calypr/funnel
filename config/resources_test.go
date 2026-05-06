package config

import (
	"math"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
)

// Helper for float comparison
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestParseCpus(t *testing.T) {
	tests := []struct {
		input   string
		want    int32
		wantErr bool
	}{
		{"", 0, false},
		{"100m", 1, false},
		{"500m", 1, false},
		{"1.5", 2, false},
		{"2", 2, false},
		{"1e3", 1000, false}, // Scientific notation support
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseCpus(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCpus(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseCpus(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		input   string
		want    float64 // in GiB (input / 1024^3)
		wantErr bool
	}{
		{"", 0, false},
		{"1073741824", 1.0, false}, // Exactly 1GiB in bytes
		{"512Mi", 0.5, false},
		{"1Gi", 1.0, false},
		{"1Ti", 1024.0, false},
		// Decimal 'G' is 1,000,000,000 bytes.
		// 1,000,000,000 / 1024^3 = 0.9313225746154785
		{"1G", 0.9313225746154785, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMemory(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMemory(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseMemory(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestApplyDefaultResources(t *testing.T) {
	k8s := &KubernetesResources{
		Defaults: &ResourceDefaults{
			Cpus:   "2",
			RamGb:  "4Gi",
			DiskGb: "100Gi",
		},
	}

	t.Run("millicore and mebibyte defaults", func(t *testing.T) {
		task := &tes.Resources{}
		k8sMilli := &KubernetesResources{
			Defaults: &ResourceDefaults{
				Cpus:   "500m",
				RamGb:  "512Mi",
				DiskGb: "1Gi",
			},
		}
		got := ApplyDefaultResources(task, k8sMilli)

		// 500m should be 1 core
		if got.CpuCores != 1 {
			t.Errorf("CpuCores = %v, want 1", got.CpuCores)
		}
		// 512Mi should be 0.5 GiB
		if got.RamGb != 0.5 {
			t.Errorf("RamGb = %v, want 0.5", got.RamGb)
		}
		if got.DiskGb != 1 {
			t.Errorf("DiskGb = %v, want 1", got.DiskGb)
		}
	})

	t.Run("partial values", func(t *testing.T) {
		// User provided Ram, but needs CPU and Disk defaults
		task := &tes.Resources{
			RamGb: 16,
		}
		got := ApplyDefaultResources(task, k8s)
		if got.CpuCores != 2 {
			t.Errorf("CpuCores = %v, want 2", got.CpuCores)
		}
		if got.RamGb != 16 {
			t.Errorf("RamGb = %v, want 16 (user value preserved)", got.RamGb)
		}
		if got.DiskGb != 100 {
			t.Errorf("DiskGb = %v, want 100", got.DiskGb)
		}
	})
}
