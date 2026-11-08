package events

import (
	"context"
	"sync"

	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/internal/taskmodel"
	"github.com/calypr/funnel/tes"
)

// TaskBuilder aggregates events into an in-memory Task object.
// Read Task only after all event writers have finished.
type TaskBuilder struct {
	*tes.Task
	mu sync.Mutex
}

// WriteEvent updates the Task object.
func (tb *TaskBuilder) WriteEvent(ctx context.Context, ev *Event) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	logger := logger.NewLogger("TaskBuilder", logger.DefaultConfig())
	t := tb.Task
	t.Id = ev.Id
	attempt := int(ev.Attempt)
	index := int(ev.Index)

	switch ev.Type {
	case Type_TASK_STATE:
		to := ev.GetState()
		if err := taskmodel.ValidateTransition(t.GetState(), ev.GetState()); err != nil {
			logger.Debug("TaskBuilder: invalid state transition", "taskID", t.Id, "from", t.GetState(), "to", ev.GetState(), "error", err)
			return err
		}
		t.State = to

	case Type_SYSTEM_LOG:
		taskmodel.
			GetTaskLog(t, attempt).SystemLogs = append(taskmodel.GetTaskLog(t, attempt).SystemLogs, ev.SysLogString())

	case Type_TASK_START_TIME:
		taskmodel.
			GetTaskLog(t, attempt).StartTime = ev.GetStartTime()

	case Type_TASK_END_TIME:
		taskmodel.
			GetTaskLog(t, attempt).EndTime = ev.GetEndTime()

	case Type_TASK_OUTPUTS:
		taskmodel.
			GetTaskLog(t, attempt).Outputs = ev.GetOutputs().Value

	case Type_TASK_METADATA:
		if taskmodel.GetTaskLog(t, attempt).Metadata == nil {
			taskmodel.
				GetTaskLog(t, attempt).Metadata = map[string]string{}
		}
		for k, v := range ev.GetMetadata().Value {
			taskmodel.
				GetTaskLog(t, attempt).Metadata[k] = v
		}

	case Type_EXECUTOR_START_TIME:
		taskmodel.
			GetExecLog(t, attempt, index).StartTime = ev.GetStartTime()

	case Type_EXECUTOR_END_TIME:
		taskmodel.
			GetExecLog(t, attempt, index).EndTime = ev.GetEndTime()

	case Type_EXECUTOR_EXIT_CODE:
		taskmodel.
			GetExecLog(t, attempt, index).ExitCode = ev.GetExitCode()

	case Type_EXECUTOR_STDOUT:
		taskmodel.
			GetExecLog(t, attempt, index).Stdout = ev.GetStdout()

	case Type_EXECUTOR_STDERR:
		taskmodel.
			GetExecLog(t, attempt, index).Stderr = ev.GetStderr()
	}

	return nil
}

func (tb *TaskBuilder) Close() {}
