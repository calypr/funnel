package datastore

import (
	"context"
	"fmt"

	"cloud.google.com/go/datastore"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

// WriteEvent writes a task event to the database.
func (d *Datastore) WriteEvent(ctx context.Context, e *events.Event) error {

	switch e.Type {

	case events.Type_TASK_CREATED:
		putKeys, putData := marshalTask(e.GetTask(), ctx)
		_, err := d.client.PutMulti(ctx, putKeys, putData)
		return err

	case events.Type_TASK_STATE:
		return d.taskUpdateInTransaction(ctx, e, updateState)

	case events.Type_SYSTEM_LOG:
		return d.appendTaskSystemLog(ctx, e)

	case events.Type_EXECUTOR_STDOUT:
		_, err := d.client.Put(ctx, stdoutKey(e.Id, e.Attempt, e.Index), marshalEvent(e))
		return err

	case events.Type_EXECUTOR_STDERR:
		_, err := d.client.Put(ctx, stderrKey(e.Id, e.Attempt, e.Index), marshalEvent(e))
		return err

	default:
		return d.taskUpdateInTransaction(ctx, e, updateTaskLog)
	}
}

type taskUpdater func(ctx context.Context, task *task, e *events.Event) error

func (d *Datastore) taskUpdateInTransaction(ctx context.Context, event *events.Event, update taskUpdater) error {
	_, err := d.client.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		task := &task{}
		taskKey := taskKey(event.Id)

		if err := tx.Get(taskKey, task); err == datastore.ErrNoSuchEntity {
			return tes.ErrNotFound
		} else if err != nil {
			return err
		} else if !server.GetUser(ctx).IsAccessible(task.Owner) {
			return tes.ErrNotPermitted
		}

		if err := update(ctx, task, event); err != nil {
			return err
		}

		_, err := tx.Put(taskKey, task)
		return err
	})
	return err
}

// This method is focused on updating the State of a Task.
// In Datastore, the whole Task-entity is updated, though just one field changes.
func updateState(ctx context.Context, task *task, e *events.Event) error {
	from := tes.State(task.State)
	to := e.GetState()
	if err := tes.ValidateTransition(from, to); err != nil {
		return err
	}
	task.State = int32(to.Number())
	return nil
}

// This method is focused on adding/updating a TaskLog of a Task.
// In Datastore, the whole Task-entity gets updated.
func updateTaskLog(ctx context.Context, task *task, e *events.Event) error {
	targetLog := getTaskLog(task, e)

	switch e.Type {

	case events.Type_TASK_START_TIME:
		targetLog.StartTime = e.GetStartTime()

	case events.Type_TASK_END_TIME:
		targetLog.EndTime = e.GetEndTime()

	case events.Type_TASK_OUTPUTS:
		targetLog.Outputs = e.GetOutputs().Value

	case events.Type_TASK_METADATA:
		targetLog.Metadata = mergeKvs(targetLog.Metadata, e.GetMetadata().Value)

	case events.Type_EXECUTOR_START_TIME:
		getExecutorLog(targetLog, e).StartTime = e.GetStartTime()

	case events.Type_EXECUTOR_END_TIME:
		getExecutorLog(targetLog, e).EndTime = e.GetEndTime()

	case events.Type_EXECUTOR_EXIT_CODE:
		getExecutorLog(targetLog, e).ExitCode = e.GetExitCode()

	default:
		return fmt.Errorf("[Datastore] function updateTaskLog does not support event: %q", e.Type.String())
	}

	return nil
}

// This method is focused on adding/updating SystemLogs of a TaskLog.
// In Datastore, SystemLogs are stored under separate keys, so the Task-entity is not updated.
func (d *Datastore) appendTaskSystemLog(ctx context.Context, event *events.Event) error {
	_, err := d.client.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		p := &part{}
		err := tx.Get(sysLogsKey(event.Id, event.Attempt), p)
		if err != nil && err != datastore.ErrNoSuchEntity {
			return err
		}

		_, err = tx.Put(sysLogsKey(event.Id, event.Attempt), &part{
			Type:       sysLogsPart,
			Attempt:    int(event.Attempt),
			Index:      int(event.Index),
			SystemLogs: append(p.SystemLogs, event.SysLogString()),
		})
		return err
	})
	return err
}

// Retrieves the tasklog from the provided task as referenced in the event (Attempt).
// If the Attempt referes to a non-existing tasklog, it is added to task.Logs.
func getTaskLog(task *task, e *events.Event) *tasklog {
	targetLogIndex := int(e.Attempt)

	// Grow slice length if necessary
	for range targetLogIndex + 1 - len(task.TaskLogs) {
		item := tasklog{
			TaskLog:  &tes.TaskLog{},
			Metadata: []kv{},
		}
		task.TaskLogs = append(task.TaskLogs, item)
	}

	return &task.TaskLogs[targetLogIndex]
}

// Retrieves the ExecutorLog from the provided tasklog as referenced in the
// event (Index). If the Index referes to a non-existing executor log, it is
// added to taskLog.Logs.
func getExecutorLog(taskLog *tasklog, e *events.Event) *tes.ExecutorLog {
	execLogIndex := int(e.Index)

	// Grow slice length if necessary
	for j := len(taskLog.Logs); j <= execLogIndex; j++ {
		taskLog.Logs = append(taskLog.Logs, &tes.ExecutorLog{})
	}

	return taskLog.Logs[execLogIndex]
}
