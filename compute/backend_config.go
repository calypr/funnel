package compute

import (
	"fmt"
	"text/template"

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

// ValidateSubmissionTemplate checks the syntax before accepting tasks.
func ValidateSubmissionTemplate(field, text string) error {
	if text == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	_, err := template.New(field).Parse(text)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}
