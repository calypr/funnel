package events

import (
	"context"

	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// TaskBuilder aggregates events into an in-memory Task object.
type TaskBuilder struct {
	*tes.Task
}

// WriteEvent updates the Task object.
func (tb TaskBuilder) WriteEvent(ctx context.Context, ev *Event) error {
	logger := logger.NewLogger("TaskBuilder", logger.DefaultConfig())
	t := tb.Task
	t.Id = ev.Id
	attempt := int(ev.Attempt)
	index := int(ev.Index)

	switch ev.Type {
	case Type_TASK_STATE:
		to := ev.GetState()
		if err := tes.ValidateTransition(t.GetState(), ev.GetState()); err != nil {
			logger.Debug("TaskBuilder: invalid state transition", "taskID", t.Id, "from", t.GetState(), "to", ev.GetState(), "error", err)
			return err
		}
		t.State = to

	case Type_SYSTEM_LOG:
		t.GetTaskLog(attempt).SystemLogs = append(t.GetTaskLog(attempt).SystemLogs, ev.SysLogString())

	case Type_TASK_START_TIME:
		t.GetTaskLog(attempt).StartTime = ev.GetStartTime()

	case Type_TASK_END_TIME:
		t.GetTaskLog(attempt).EndTime = ev.GetEndTime()

	case Type_TASK_OUTPUTS:
		t.GetTaskLog(attempt).Outputs = ev.GetOutputs().Value

	case Type_TASK_METADATA:
		if t.GetTaskLog(attempt).Metadata == nil {
			t.GetTaskLog(attempt).Metadata = map[string]string{}
		}
		for k, v := range ev.GetMetadata().Value {
			t.GetTaskLog(attempt).Metadata[k] = v
		}

	case Type_EXECUTOR_START_TIME:
		t.GetExecLog(attempt, index).StartTime = ev.GetStartTime()

	case Type_EXECUTOR_END_TIME:
		t.GetExecLog(attempt, index).EndTime = ev.GetEndTime()

	case Type_EXECUTOR_EXIT_CODE:
		t.GetExecLog(attempt, index).ExitCode = ev.GetExitCode()

	case Type_EXECUTOR_STDOUT:
		t.GetExecLog(attempt, index).Stdout = ev.GetStdout()

	case Type_EXECUTOR_STDERR:
		t.GetExecLog(attempt, index).Stderr = ev.GetStderr()
	}

	return nil
}

func (tb TaskBuilder) Close() {}
