package tes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const gpuUUIDPattern = `(?:[0-9A-Fa-f]{32}|[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})`

var gpuDeviceIDPattern = regexp.MustCompile(`^(?:[0-9]+|GPU-` + gpuUUIDPattern + `|MIG-` + gpuUUIDPattern + `|MIG-GPU-` + gpuUUIDPattern + `/[0-9]+/[0-9]+)$`)

// GPUDevices returns validated device IDs from backend_parameters.gpu_devices.
// expectedCount may be zero when the caller does not have a separate count.
func GPUDevices(resources *Resources, expectedCount int) ([]string, error) {
	if expectedCount < 0 {
		return nil, fmt.Errorf("expected GPU count cannot be negative")
	}
	if resources == nil {
		return nil, nil
	}

	var value string
	found := false
	for name, candidate := range resources.GetBackendParameters() {
		if !strings.EqualFold(name, "gpu_devices") {
			continue
		}
		if found {
			return nil, fmt.Errorf("multiple backend parameter keys match gpu_devices")
		}
		value = candidate
		found = true
	}
	if !found {
		return nil, nil
	}
	return ParseGPUDevices(value, expectedCount)
}

// ParseGPUDevices validates a comma-separated list of Docker GPU device IDs.
// expectedCount may be zero when the caller does not have a separate count.
func ParseGPUDevices(value string, expectedCount int) ([]string, error) {
	if expectedCount < 0 {
		return nil, fmt.Errorf("expected GPU count cannot be negative")
	}
	if value == "" || strings.TrimSpace(value) != value {
		return nil, fmt.Errorf("GPU device list must be non-empty and have no surrounding whitespace")
	}

	devices := strings.Split(value, ",")
	if expectedCount > 0 && len(devices) != expectedCount {
		return nil, fmt.Errorf("GPU device list contains %d IDs, but gpu_count requests %d", len(devices), expectedCount)
	}

	seen := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		if !gpuDeviceIDPattern.MatchString(device) {
			return nil, fmt.Errorf("invalid GPU device ID %q", device)
		}
		canonicalID := strings.ToLower(device)
		if isDecimalGPUIndex(device) {
			index, err := strconv.ParseUint(device, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("GPU device index %q is out of range", device)
			}
			canonicalID = strconv.FormatUint(index, 10)
		}
		if _, duplicate := seen[canonicalID]; duplicate {
			return nil, fmt.Errorf("duplicate GPU device ID %q", device)
		}
		seen[canonicalID] = struct{}{}
	}
	return devices, nil
}

func isDecimalGPUIndex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
