package tes

import (
	"fmt"
	"strconv"
	"strings"
)

// GPUCount returns the requested GPU count, or zero if no GPU was requested.
func GPUCount(resources *Resources) (int, error) {
	var value string
	var ok bool
	for key, candidate := range resources.GetBackendParameters() {
		if strings.EqualFold(key, "gpu_count") {
			if ok {
				return 0, fmt.Errorf("duplicate backend parameter gpu_count")
			}
			value, ok = candidate, true
		}
	}
	if !ok {
		return 0, nil
	}
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, fmt.Errorf("gpu_count must be a positive integer")
	}
	count, err := strconv.Atoi(value)
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("gpu_count must be a positive integer")
	}
	return count, nil
}
