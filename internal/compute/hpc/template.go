package hpc

import (
	"fmt"
	"text/template"
)

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
