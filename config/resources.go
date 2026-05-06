package config

import (
	"fmt"
	"math"

	"github.com/ohsu-comp-bio/funnel/tes"
	"k8s.io/apimachinery/pkg/api/resource"
)

func K8sConfigToTesResources(cpu, ram, disk string) *tes.Resources {
	tr := &tes.Resources{}
	tr.CpuCores, _ = ParseCpus(cpu)
	tr.RamGb, _ = ParseMemory(ram)
	tr.DiskGb, _ = ParseMemory(disk)
	return tr
}

func ApplyDefaultResources(res *tes.Resources, k8s *KubernetesResources) *tes.Resources {
	if res == nil {
		res = &tes.Resources{}
	}
	var err error

	// Apply defaults if not specified
	// CPU Default
	if res.CpuCores == 0 {
		res.CpuCores, err = ParseCpus(k8s.Defaults.Cpus)
		if err != nil {
			res.CpuCores = 0
		}
	}

	// Ram Default
	if res.RamGb == 0 {
		res.RamGb, err = ParseMemory(k8s.Defaults.RamGb)
		if err != nil {
			res.RamGb = 0
		}
	}

	// Disk Default
	if res.DiskGb == 0 {
		res.DiskGb, err = ParseMemory(k8s.Defaults.DiskGb)
		if err != nil {
			res.DiskGb = 0
		}
	}

	return res
}

func GetResourceLimits(k8s *KubernetesResources) *tes.Resources {
	res := &tes.Resources{}
	var err error
	res.CpuCores, err = ParseCpus(k8s.Limits.Cpus)
	if err != nil {
		res.CpuCores = 0
	}
	res.RamGb, err = ParseMemory(k8s.Limits.RamGb)
	if err != nil {
		res.RamGb = 0
	}
	res.DiskGb, err = ParseMemory(k8s.Limits.DiskGb)
	if err != nil {
		res.DiskGb = 0
	}

	return res
}

// ParseCPU parses Kubernetes-style CPU values (e.g., "100m", "0.5", "2")
// ParseCpus handles both CPU (m) and RAM/Disk (Mi, Gi)
// Keeping as int32 (whole integer) to follow TES 1.1 spec (may be changed to double/float64 in TES 1.2+)
func ParseCpus(s string) (int32, error) {
	if s == "" {
		return 0, nil
	}

	// ParseQuantity handles "100m", "512Mi", "1.5", etc.
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, fmt.Errorf("invalid resource value %q: %v", s, err)
	}

	// For CPU, convert to a float and then round up to whole cores (e.g., "500m" -> 1).
	// AsApproximateFloat64 is safe for these resource ranges.
	cpuFloat := q.AsApproximateFloat64()

	return int32(math.Ceil(cpuFloat)), nil
}

// ParseMemory parses Kubernetes-style memory values (e.g., "512Mi", "1Gi", "1000")
// and returns the value in Gigabytes (float64).
func ParseMemory(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}

	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value %q: %v", s, err)
	}

	// q.Value() returns the quantity as an int64 number of bytes.
	// We divide by 1024^3 to get Gibibytes (GiB),
	// which is the standard "GB" used in K8s contexts.
	bytes := float64(q.Value())
	gb := bytes / (1024 * 1024 * 1024)

	return gb, nil
}
