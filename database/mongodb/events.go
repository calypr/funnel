package mongodb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// WriteEvent creates an event for the server to handle.
func (db *MongoDB) WriteEvent(ctx context.Context, req *events.Event) error {
	tasks := db.tasks()

	update := bson.M{}
	selector := bson.M{"id": req.Id}

	switch req.Type {
	case events.Type_TASK_CREATED:
		task := req.GetTask()
		task.State = tes.State_QUEUED
		task.Logs = []*tes.TaskLog{
			{
				Logs:       []*tes.ExecutorLog{},
				Metadata:   map[string]string{},
				SystemLogs: []string{},
			},
		}
		return db.insertTask(ctx, task)

	case events.Type_TASK_STATE:
		return db.transitionReservedTask(ctx, req.Id, req.GetState())

	case events.Type_TASK_START_TIME:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.starttime", req.Attempt): req.GetStartTime(),
			},
		}

	case events.Type_TASK_END_TIME:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.endtime", req.Attempt): req.GetEndTime(),
			},
		}

	case events.Type_TASK_OUTPUTS:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.outputs", req.Attempt): req.GetOutputs().Value,
			},
		}

	case events.Type_TASK_METADATA:
		metadataUpdate := bson.M{}
		for k, v := range req.GetMetadata().Value {
			metadataUpdate[fmt.Sprintf("logs.%v.metadata.%s", req.Attempt, k)] = v
		}
		update = bson.M{"$set": metadataUpdate}

	case events.Type_EXECUTOR_START_TIME:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.logs.%v.starttime", req.Attempt, req.Index): req.GetStartTime(),
			},
		}

	case events.Type_EXECUTOR_END_TIME:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.logs.%v.endtime", req.Attempt, req.Index): req.GetEndTime(),
			},
		}

	case events.Type_EXECUTOR_EXIT_CODE:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.logs.%v.exitcode", req.Attempt, req.Index): req.GetExitCode(),
			},
		}

	case events.Type_EXECUTOR_STDOUT:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.logs.%v.stdout", req.Attempt, req.Index): req.GetStdout(),
			},
		}

	case events.Type_EXECUTOR_STDERR:
		update = bson.M{
			"$set": bson.M{
				fmt.Sprintf("logs.%v.logs.%v.stderr", req.Attempt, req.Index): req.GetStderr(),
			},
		}

	case events.Type_SYSTEM_LOG:
		update = bson.M{
			"$push": bson.M{
				fmt.Sprintf("logs.%v.systemlogs", req.Attempt): req.SysLogString(),
			},
		}
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	result, err := tasks.UpdateOne(mctx, selector, update)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return tes.ErrNotFound
	}
	return nil
}

func (db *MongoDB) insertTask(ctx context.Context, task *tes.Task) error {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	_, err := db.tasks().InsertOne(mctx, &taskRecord{Task: task, Owner: server.GetUsername(ctx)})
	return err
}
