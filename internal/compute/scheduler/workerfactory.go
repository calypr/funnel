package scheduler

import (
	"context"
)

// Worker is a function which creates a new worker instance.
type Worker func(ctx context.Context, taskID string) error

// NoopWorker does nothing.
func NoopWorker(ctx context.Context, taskID string) error {
	return nil
}
