package elastic

import (
	"context"
	"encoding/json"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/result"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/scriptlanguage"
)

var updateTaskLogs = `
if (ctx._source.logs == null) {
  ctx._source.logs = new ArrayList();
}

// Ensure the task logs array is long enough.
for (; params.attempt > ctx._source.logs.length - 1; ) {
  Map m = new HashMap();
  m.logs = new ArrayList();
  ctx._source.logs.add(m);
}

// Set the field.
if (params.field == "system_logs") {
  if (ctx._source.logs[params.attempt].system_logs == null) {
    ctx._source.logs[params.attempt].system_logs = new ArrayList();
  }
  ctx._source.logs[params.attempt].system_logs.add(params.value)
} else if (params.field == "metadata") {
  if (ctx._source.logs[params.attempt].metadata == null) {
    ctx._source.logs[params.attempt].metadata = new HashMap();
  }
  for (entry in params.value.entrySet()) {
    ctx._source.logs[params.attempt].metadata.put(entry.getKey(), entry.getValue())
  }
} else {
  ctx._source.logs[params.attempt][params.field] = params.value;
}
`

var updateExecutorLogs = `
if (ctx._source.logs == null) {
  ctx._source.logs = new ArrayList();
}

// Ensure the task logs array is long enough.
for (; params.attempt > ctx._source.logs.length - 1; ) {
  Map m = new HashMap();
  m.logs = new ArrayList();
  ctx._source.logs.add(m);
}

// Ensure the executor logs array is long enough.
for (; params.index > ctx._source.logs[params.attempt].logs.length - 1; ) {
  Map m = new HashMap();
  ctx._source.logs[params.attempt].logs.add(m);
}

// Set the field.
ctx._source.logs[params.attempt].logs[params.index][params.field] = params.value;
`

func asRawMessage(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

func taskLogUpdate(attempt uint32, field string, value interface{}) *types.Script {
	return &types.Script{
		Lang:   &scriptlanguage.Painless,
		Source: &updateTaskLogs,
		Params: map[string]json.RawMessage{
			"attempt": asRawMessage(attempt),
			"field":   asRawMessage(field),
			"value":   asRawMessage(value),
		},
	}
}

func execLogUpdate(attempt, index uint32, field string, value interface{}) *types.Script {
	return &types.Script{
		Lang:   &scriptlanguage.Painless,
		Source: &updateExecutorLogs,
		Params: map[string]json.RawMessage{
			"attempt": asRawMessage(attempt),
			"index":   asRawMessage(index),
			"field":   asRawMessage(field),
			"value":   asRawMessage(value),
		},
	}
}

// WriteEvent writes a task update event.
func (es *Elastic) WriteEvent(ctx context.Context, ev *events.Event) error {
	u := es.client.Update(es.taskIndex, ev.Id).RetryOnConflict(10)

	switch ev.Type {
	case events.Type_TASK_CREATED:
		task := ev.GetTask()

		res, err := es.client.Index(es.taskIndex).
			Id(task.Id).
			Document(task).
			Do(ctx)

		if err == nil {
			_, err = es.client.Update(res.Index_, res.Id_).
				IfSeqNo(int64ToStr(res.SeqNo_)).
				IfPrimaryTerm(int64ToStr(res.PrimaryTerm_)).
				Doc(map[string]string{"owner": server.GetUsername(ctx)}).
				Do(ctx)
		}

		return err

	case events.Type_TASK_STATE:
		retrier := util.NewRetrier()
		retrier.ShouldRetry = func(err error) bool {
			_, isTransitionError := err.(*tes.TransitionError)
			return !isTransitionError && err != tes.ErrNotFound && err != tes.ErrNotPermitted
		}

		return retrier.Retry(ctx, func() error {
			// get current state & version
			task, seqNo, primaryTerm, err := es.getTask(ctx, &tes.GetTaskRequest{
				Id:   ev.Id,
				View: tes.View_MINIMAL.String(),
			})
			if err != nil {
				return err
			}

			// validate state transition
			from := task.State
			to := ev.GetState()
			if err := tes.ValidateTransition(from, to); err != nil {
				return err
			}

			// apply version restriction and set update
			_, err = es.client.Update(es.taskIndex, ev.Id).
				IfSeqNo(seqNo).
				IfPrimaryTerm(primaryTerm).
				Doc(map[string]string{"state": to.String()}).
				Do(ctx)
			return err
		})

	case events.Type_TASK_START_TIME:
		u = u.Script(taskLogUpdate(ev.Attempt, "start_time", ev.GetStartTime()))

	case events.Type_TASK_END_TIME:
		u = u.Script(taskLogUpdate(ev.Attempt, "end_time", ev.GetEndTime()))

	case events.Type_TASK_OUTPUTS:
		u = u.Script(taskLogUpdate(ev.Attempt, "outputs", ev.GetOutputs().Value))

	case events.Type_TASK_METADATA:
		u = u.Script(taskLogUpdate(ev.Attempt, "metadata", ev.GetMetadata().Value))

	case events.Type_EXECUTOR_START_TIME:
		u = u.Script(execLogUpdate(ev.Attempt, ev.Index, "start_time", ev.GetStartTime()))

	case events.Type_EXECUTOR_END_TIME:
		u = u.Script(execLogUpdate(ev.Attempt, ev.Index, "end_time", ev.GetEndTime()))

	case events.Type_EXECUTOR_EXIT_CODE:
		u = u.Script(execLogUpdate(ev.Attempt, ev.Index, "exit_code", ev.GetExitCode()))

	case events.Type_EXECUTOR_STDOUT:
		u = u.Script(execLogUpdate(ev.Attempt, ev.Index, "stdout", ev.GetStdout()))

	case events.Type_EXECUTOR_STDERR:
		u = u.Script(execLogUpdate(ev.Attempt, ev.Index, "stderr", ev.GetStderr()))

	case events.Type_SYSTEM_LOG:
		u = u.Script(taskLogUpdate(ev.Attempt, "system_logs", ev.SysLogString()))
	}

	resp, err := u.Do(ctx)
	if resp.Result == result.Noop || resp.Result == result.Notfound {
		return tes.ErrNotFound
	}
	return err
}
