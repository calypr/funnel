package badger

import (
	"context"
	"fmt"
	"time"

	badger "github.com/dgraph-io/badger/v2"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util"
	proto "google.golang.org/protobuf/proto"
)

// WriteEvent creates an event for the server to handle.
func (db *Badger) WriteEvent(ctx context.Context, req *events.Event) error {
	r := util.Retrier{
		InitialInterval:     1 * time.Millisecond,
		MaxInterval:         10 * time.Second,
		MaxElapsedTime:      5 * time.Minute,
		Multiplier:          1.5,
		RandomizationFactor: 0.5,
		MaxTries:            50,
		ShouldRetry: func(err error) bool {
			_, isTransitionError := err.(*tes.TransitionError)
			return !isTransitionError && err != tes.ErrNotFound && err != tes.ErrNotPermitted
		},
	}

	return r.Retry(ctx, func() error {
		return db.writeEvent(ctx, req)
	})
}

func (db *Badger) writeEvent(ctx context.Context, req *events.Event) error {

	// It's important this error be returned directly without being wrapped,
	// because the retrier's ShouldRetry needs to check the error type (above).
	return db.db.Update(func(txn *badger.Txn) error {

		// If this event creates a new task, we don't need to update logic below,
		// just marshal and save the task.
		if req.Type == events.Type_TASK_CREATED {
			task := req.GetTask()
			val, err := proto.Marshal(task)
			if err != nil {
				return fmt.Errorf("marshaling task to bytes: %s", err)
			}

			// Store the username as task owner:
			err = txn.Set(ownerKey(task.Id), []byte(server.GetUsername(ctx)))
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
			if err = tes.ValidateTransition(from, to); err != nil {
				return err
			}
			task.State = to

		case events.Type_TASK_START_TIME:
			task.GetTaskLog(0).StartTime = req.GetStartTime()

		case events.Type_TASK_END_TIME:
			task.GetTaskLog(0).EndTime = req.GetEndTime()

		case events.Type_TASK_OUTPUTS:
			task.GetTaskLog(0).Outputs = req.GetOutputs().Value

		case events.Type_TASK_METADATA:
			meta := req.GetMetadata().Value
			tl := task.GetTaskLog(0)
			if tl.Metadata == nil {
				tl.Metadata = map[string]string{}
			}
			for k, v := range meta {
				tl.Metadata[k] = v
			}

		case events.Type_EXECUTOR_START_TIME:
			task.GetExecLog(0, int(req.Index)).StartTime = req.GetStartTime()

		case events.Type_EXECUTOR_END_TIME:
			task.GetExecLog(0, int(req.Index)).EndTime = req.GetEndTime()

		case events.Type_EXECUTOR_EXIT_CODE:
			task.GetExecLog(0, int(req.Index)).ExitCode = req.GetExitCode()

		case events.Type_EXECUTOR_STDOUT:
			task.GetExecLog(0, int(req.Index)).Stdout = req.GetStdout()

		case events.Type_EXECUTOR_STDERR:
			task.GetExecLog(0, int(req.Index)).Stderr = req.GetStderr()

		case events.Type_SYSTEM_LOG:
			tl := task.GetTaskLog(0)
			tl.SystemLogs = append(tl.SystemLogs, req.SysLogString())
		}

		val, err := proto.Marshal(task)
		if err != nil {
			return fmt.Errorf("marshaling task to bytes: %s", err)
		}

		return txn.Set(taskKey(task.Id), val)
	})
}
