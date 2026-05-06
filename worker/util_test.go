package worker

import (
	"fmt"
	"testing"
)

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "nil error returns 0",
			err:      nil,
			expected: 0,
		},
		{
			name:     "generic error returns -999",
			err:      fmt.Errorf("some generic error"),
			expected: -999,
		},
		{
			name:     "kubernetes error with exit code 127",
			err:      fmt.Errorf("executor job test-123-0 failed with exit code 127 (Error): command not found"),
			expected: 127,
		},
		{
			name:     "kubernetes error with exit code 1",
			err:      fmt.Errorf("executor job test-123-0 failed with exit code 1 (Error): permission denied"),
			expected: 1,
		},
		{
			name:     "kubernetes error without exit code",
			err:      fmt.Errorf("executor job test-123-0 failed with some other message"),
			expected: -999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getExitCode(tt.err)

			if err != nil {
				t.Errorf("getExitCode() returned unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("getExitCode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
