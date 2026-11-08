package tes

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/funnel/tes"
)

// WaitForTask polls /v1/tasks/{id} for each Id provided and returns
// once all tasks are in a terminal state.
func (c *Client) WaitForTask(ctx context.Context, taskIDs ...string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		done := true
		for _, id := range taskIDs {
			task, err := c.GetTask(ctx, &tes.GetTaskRequest{Id: id, View: tes.View_MINIMAL.String()})
			if err != nil {
				return err
			}
			switch task.State {
			case tes.State_COMPLETE:
			case tes.State_EXECUTOR_ERROR, tes.State_SYSTEM_ERROR, tes.State_CANCELED:
				return fmt.Errorf("Task %s exited with state %s", id, task.State.String())
			default:
				done = false
			}
		}
		if done {
			return nil
		}
	}
}
