package task

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/funnel/api/tes"
)

// NormalizeBackendParameters rejects ambiguous keys and canonicalizes TES keys.
func NormalizeBackendParameters(resources *tes.Resources) error {
	if resources == nil {
		return nil
	}
	normalized := make(map[string]string, len(resources.BackendParameters))
	for key, value := range resources.BackendParameters {
		key = strings.ToLower(key)
		if key == "" {
			return fmt.Errorf("backend parameter key must not be empty")
		}
		if _, exists := normalized[key]; exists {
			return fmt.Errorf("duplicate backend parameter %q", key)
		}
		normalized[key] = value
	}
	resources.BackendParameters = normalized
	return nil
}

// FilterBackendParameters removes unsupported optional parameters and returns their keys.
func FilterBackendParameters(resources *tes.Resources, supported []string) ([]string, error) {
	if err := NormalizeBackendParameters(resources); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(supported))
	for _, key := range supported {
		allowed[strings.ToLower(key)] = true
	}
	var unsupported []string
	for key := range resources.GetBackendParameters() {
		if !allowed[key] {
			unsupported = append(unsupported, key)
		}
	}
	sort.Strings(unsupported)
	if len(unsupported) > 0 && resources.GetBackendParametersStrict() {
		return nil, fmt.Errorf("backend parameters not supported: %s", strings.Join(unsupported, ", "))
	}
	for _, key := range unsupported {
		delete(resources.BackendParameters, key)
	}
	return unsupported, nil
}
