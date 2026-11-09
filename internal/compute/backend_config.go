package compute

import (
	"fmt"

	"google.golang.org/protobuf/types/known/durationpb"
)

// ValidateReconcileRate checks the interval used by an enabled reconciler.
func ValidateReconcileRate(field string, disabled bool, interval *durationpb.Duration) error {
	if disabled {
		return nil
	}
	if interval == nil {
		return fmt.Errorf("%s must be a positive duration when reconciliation is enabled", field)
	}
	err := interval.CheckValid()
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if interval.AsDuration() <= 0 {
		return fmt.Errorf("%s must be a positive duration when reconciliation is enabled", field)
	}
	return nil
}
