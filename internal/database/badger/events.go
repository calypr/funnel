package badger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/identity"
	taskpolicy "github.com/calypr/funnel/internal/task"
	"github.com/cenkalti/backoff/v7"
	badger "github.com/dgraph-io/badger/v4"
	proto "google.golang.org/protobuf/proto"
)

var errTaskExists = errors.New("task already exists")

// WriteEvent creates an event for the server to handle.
func (db *Badger) WriteEvent(ctx context.Context, req *events.Event) error {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = time.Millisecond
	b.MaxInterval = 10 * time.Second
	b.Multiplier = 1.5
	b.RandomizationFactor = 0.5

	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		err := db.writeEvent(ctx, req)
		if err != nil {
			_, isTransitionError := err.(*taskpolicy.TransitionError)
			if isTransitionError || err == taskpolicy.ErrNotFound || err == taskpolicy.ErrNotPermitted || err == errTaskExists {
				return struct{}{}, backoff.Permanent(err)
			}
		}
		return struct{}{}, err
	},
		backoff.WithBackOff(b),
		backoff.WithMaxElapsedTime(5*time.Minute),
		backoff.WithMaxTries(50),
	)
	if err == nil {
		return nil
	}

	retryErr := backoff.AsRetryError(err)
	if ctx.Err() != nil && errors.Is(retryErr.Cause, context.Cause(ctx)) {
		return ctx.Err()
	}
	return retryErr.LastErr
}

func (db *Badger) writeEvent(ctx context.Context, req *events.Event) error {

	// Keep terminal errors unwrapped so WriteEvent can classify them directly.
	return db.db.Update(func(txn *badger.Txn) error {

		// If this event creates a new task, we don't need to update logic below,
		// just marshal and save the task.
		if req.Type == events.Type_TASK_CREATED {
			task := req.GetTask()
			if _, err := txn.Get(taskKey(task.Id)); err == nil {
				return errTaskExists
			} else if err != badger.ErrKeyNotFound {
				return err
			}
			val, err := proto.Marshal(task)
			if err != nil {
				return fmt.Errorf("marshaling task to bytes: %s", err)
			}

			// Store the username as task owner:
			err = txn.Set(ownerKey(task.Id), []byte(identity.GetUsername(ctx)))
			if err != nil {
				return fmt.Errorf("storing owner info: %s", err)
			}

			return txn.Set(taskKey(task.Id), val)
		}

		// The rest of the events below all update a task, so we need to make sure it exists.
		task, err := getTask(txn, req.Id, ctx)
		if err != nil {
			return err
		}

		switch req.Type {
		case events.Type_TASK_STATE:
			from := task.State
			to := req.GetState()
			if err = taskpolicy.ValidateTransition(from, to); err != nil {
				return err
			}
			task.State = to

		case events.Type_TASK_START_TIME:
			taskpolicy.
				GetTaskLog(task, 0).StartTime = req.GetStartTime()

		case events.Type_TASK_END_TIME:
			taskpolicy.
				GetTaskLog(task, 0).EndTime = req.GetEndTime()

		case events.Type_TASK_OUTPUTS:
			taskpolicy.
				GetTaskLog(task, 0).Outputs = req.GetOutputs().Value

		case events.Type_TASK_METADATA:
			meta := req.GetMetadata().Value
			tl := taskpolicy.GetTaskLog(task, 0)
			if tl.Metadata == nil {
				tl.Metadata = map[string]string{}
			}
			for k, v := range meta {
				tl.Metadata[k] = v
			}

		case events.Type_EXECUTOR_START_TIME:
			taskpolicy.
				GetExecLog(task, 0, int(req.Index)).StartTime = req.GetStartTime()

		case events.Type_EXECUTOR_END_TIME:
			taskpolicy.
				GetExecLog(task, 0, int(req.Index)).EndTime = req.GetEndTime()

		case events.Type_EXECUTOR_EXIT_CODE:
			taskpolicy.
				GetExecLog(task, 0, int(req.Index)).ExitCode = req.GetExitCode()

		case events.Type_EXECUTOR_STDOUT:
			taskpolicy.
				GetExecLog(task, 0, int(req.Index)).Stdout = req.GetStdout()

		case events.Type_EXECUTOR_STDERR:
			taskpolicy.
				GetExecLog(task, 0, int(req.Index)).Stderr = req.GetStderr()

		case events.Type_SYSTEM_LOG:
			tl := taskpolicy.GetTaskLog(task, 0)
			tl.SystemLogs = append(tl.SystemLogs, req.SysLogString())
		}

		val, err := proto.Marshal(task)
		if err != nil {
			return fmt.Errorf("marshaling task to bytes: %s", err)
		}

		return txn.Set(taskKey(task.Id), val)
	})
}
