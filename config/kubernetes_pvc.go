package config

import "fmt"

// Per-task storage provisioning modes. See Kubernetes.PVCMode.
const (
	// PVCModeFull creates a dedicated PV + PVC per task, statically bound.
	PVCModeFull = "full"
	// PVCModePVC creates only a dedicated PVC per task, dynamically
	// provisioned via StorageClassName.
	PVCModePVC = "pvc"
	// PVCModeShared creates nothing per task; every task mounts the single,
	// pre-existing shared PVC (SharedPVCName), isolated via subPath.
	PVCModeShared = "shared"

	// DefaultSharedPVCName is used when SharedPVCName is empty.
	DefaultSharedPVCName = "funnel-pvc"
)

// GetPVCModeOrDefault returns the configured PVCMode, treating an empty
// value as PVCModeFull.
func (k *Kubernetes) GetPVCModeOrDefault() string {
	if k.GetPVCMode() == "" {
		return PVCModeFull
	}
	return k.GetPVCMode()
}

// ValidatePVCMode checks that PVCMode and its dependent settings are valid.
func (k *Kubernetes) ValidatePVCMode() error {
	switch mode := k.GetPVCModeOrDefault(); mode {
	case PVCModeFull, PVCModeShared:
		return nil
	case PVCModePVC:
		if k.GetStorageClassName() == "" {
			return fmt.Errorf("Kubernetes.StorageClassName is required when PVCMode is %q", PVCModePVC)
		}
		return nil
	default:
		return fmt.Errorf("Kubernetes.PVCMode must be one of %q, %q, %q (got %q)",
			PVCModeFull, PVCModePVC, PVCModeShared, mode)
	}
}

// PVCNameForTask returns the name of the PVC that a task's worker/executor
// pods mount. In PVCModeShared every task shares the same pre-existing PVC;
// otherwise each task gets its own PVC (see config/kubernetes/worker-pvc.yaml).
func (k *Kubernetes) PVCNameForTask(taskID string) string {
	if k.GetPVCModeOrDefault() == PVCModeShared {
		if k.GetSharedPVCName() != "" {
			return k.GetSharedPVCName()
		}
		return DefaultSharedPVCName
	}
	return fmt.Sprintf("funnel-worker-pvc-%s", taskID)
}

// CreatesPV reports whether a dedicated (cluster-scoped) PV is created per
// task, which is only the case in PVCModeFull.
func (k *Kubernetes) CreatesPV() bool {
	return k.GetPVCModeOrDefault() == PVCModeFull
}

// CreatesPVC reports whether a dedicated PVC is created per task, which is
// the case in every mode except PVCModeShared.
func (k *Kubernetes) CreatesPVC() bool {
	return k.GetPVCModeOrDefault() != PVCModeShared
}
