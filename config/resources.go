package config

import (
	"fmt"
	"math"

	"github.com/ohsu-comp-bio/funnel/tes"
	"k8s.io/apimachinery/pkg/api/resource"
)

type ResourceValidationError struct {
	Field     string
	Requested float64
	Limit     float64
}

func (e *ResourceValidationError) Error() string {
	return fmt.Sprintf("requested %s %.2f exceeds limit %.2f", e.Field, e.Requested, e.Limit)
}

func ValidateResources(res *tes.Resources, k8s *KubernetesResources) (*tes.Resources, error) {
	res = applyDefaults(res, k8s)
	limits := GetResourceLimits(k8s)

	if limits.CpuCores > 0 && res.CpuCores > limits.CpuCores {
		return nil, &ResourceValidationError{Field: "cpu_cores", Requested: float64(res.CpuCores), Limit: float64(limits.CpuCores)}
	}
	if limits.RamGb > 0 && res.RamGb > limits.RamGb {
		return nil, &ResourceValidationError{Field: "ram_gb", Requested: res.RamGb, Limit: limits.RamGb}
	}
	if limits.DiskGb > 0 && res.DiskGb > limits.DiskGb {
		return nil, &ResourceValidationError{Field: "disk_gb", Requested: res.DiskGb, Limit: limits.DiskGb}
	}

	return res, nil
}

func GetResourceLimits(k8s *KubernetesResources) *tes.Resources {
	res := &tes.Resources{}
	if k8s == nil || k8s.Limits == nil {
		return res
	}
	var err error
	res.CpuCores, err = parseCpus(k8s.Limits.Cpus)
	if err != nil {
		res.CpuCores = 0
	}
	res.RamGb, err = parseMemory(k8s.Limits.RamGb)
	if err != nil {
		res.RamGb = 0
	}
	res.DiskGb, err = parseMemory(k8s.Limits.DiskGb)
	if err != nil {
		res.DiskGb = 0
	}

	return res
}

func applyDefaults(res *tes.Resources, k8s *KubernetesResources) *tes.Resources {
	if res == nil {
		res = &tes.Resources{}
	}
	if k8s == nil || k8s.Defaults == nil {
		return res
	}
	var err error

	// Apply defaults if not specified
	// CPU Default
	if res.CpuCores == 0 {
		res.CpuCores, err = parseCpus(k8s.Defaults.Cpus)
		if err != nil {
			res.CpuCores = 0
		}
	}

	// Ram Default
	if res.RamGb == 0 {
		res.RamGb, err = parseMemory(k8s.Defaults.RamGb)
		if err != nil {
			res.RamGb = 0
		}
	}

	// Disk Default
	if res.DiskGb == 0 {
		res.DiskGb, err = parseMemory(k8s.Defaults.DiskGb)
		if err != nil {
			res.DiskGb = 0
		}
	}

	return res
}

// parseCpus handles both CPU (m) and RAM/Disk (Mi, Gi)
// Keeping as int32 (whole integer) to follow TES 1.1 spec (may be changed to double/float64 in TES 1.2+)
func parseCpus(s string) (int32, error) {
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

// parseMemory parses Kubernetes-style memory values (e.g., "512Mi", "1Gi", "1000")
// and returns the value in Gigabytes (float64).
func parseMemory(s string) (float64, error) {
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
