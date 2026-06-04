package worker

import (
	"fmt"
	"testing"
)

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		expected  int
		expectErr bool
	}{
		{
			name:     "nil error returns 0",
			err:      nil,
			expected: 0,
		},
		{
			name:      "generic error returns 1",
			err:       fmt.Errorf("some generic error"),
			expected:  1,
			expectErr: true,
		},
		{
			name:     "kubernetes error with exit code 127",
			err:      &K8sExecutorErr{ExitCode: 127, Reason: "Error", Message: "command not found", JobName: "test-123-0"},
			expected: 127,
		},
		{
			name:     "kubernetes error with exit code 1",
			err:      &K8sExecutorErr{ExitCode: 1, Reason: "Error", Message: "permission denied", JobName: "test-123-0"},
			expected: 1,
		},
		{
			name:     "kubernetes error without exit code",
			err:      &K8sExecutorErr{ExitCode: 0, Reason: "Error", Message: "some other message", JobName: "test-123-0"},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getExitCode(tt.err)

			if tt.expectErr && err == nil {
				t.Errorf("getExitCode() expected an error but got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("getExitCode() returned unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("getExitCode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
