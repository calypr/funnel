package kubernetes

import (
	"fmt"
	"strings"

	"github.com/calypr/funnel/tes"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

// DefaultGPUResource is the standard extended resource name advertised by
// NVIDIA device plugins.
const DefaultGPUResource = "nvidia.com/gpu"

// GPUOptions describes the accelerator request carried by a task's backend
// parameters. A zero count means that the task does not request a GPU.
type GPUOptions struct {
	Count        int64
	ResourceName string
	NodeSelector map[string]string
}

// ParseGPUOptions validates GPU backend parameters before they are persisted
// and returns their typed form for executor Job construction.
func ParseGPUOptions(parameters map[string]string) (GPUOptions, error) {
	options := GPUOptions{ResourceName: DefaultGPUResource}
	resources := &tes.Resources{BackendParameters: parameters}
	if err := tes.NormalizeBackendParameters(resources); err != nil {
		return GPUOptions{}, err
	}
	parameters = resources.BackendParameters
	count, err := tes.GPUCount(resources)
	if err != nil {
		return GPUOptions{}, err
	}
	options.Count = int64(count)

	if raw, exists := parameters[gpuResourceBackendParameter]; exists {
		if !strings.Contains(raw, "/") {
			return GPUOptions{}, fmt.Errorf("gpu_resource %q must be a domain-prefixed extended resource name", raw)
		}
		if strings.Contains(raw, "kubernetes.io/") || strings.HasPrefix(raw, "requests.") {
			return GPUOptions{}, fmt.Errorf("gpu_resource %q uses a reserved Kubernetes resource prefix", raw)
		}
		if problems := validation.IsQualifiedName(raw); len(problems) > 0 {
			return GPUOptions{}, fmt.Errorf("gpu_resource %q is invalid: %s", raw, strings.Join(problems, ", "))
		}
		options.ResourceName = raw
	}

	if raw, exists := parameters[gpuNodeSelectorBackendParameter]; exists {
		selector, err := labels.ConvertSelectorToLabelsMap(raw)
		if err != nil {
			return GPUOptions{}, fmt.Errorf("gpu_node_selector must use comma-separated key=value labels: %w", err)
		}
		options.NodeSelector = selector
	}
	return options, nil
}

// ValidateGPUNodeSelector rejects task labels that would replace an existing
// administrative or template selector with a different value.
func ValidateGPUNodeSelector(selector map[string]string, existingSelectors ...map[string]string) error {
	for key, value := range selector {
		for _, existing := range existingSelectors {
			if existingValue, ok := existing[key]; ok && existingValue != value {
				return fmt.Errorf("gpu_node_selector label %q=%q conflicts with existing value %q", key, value, existingValue)
			}
		}
	}
	return nil
}
