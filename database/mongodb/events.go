package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// WriteEvent creates an event for the server to handle.
func (db *MongoDB) WriteEvent(ctx context.Context, req *events.Event) error {
	logger := logger.NewLogger("mongodb", logger.DefaultConfig())
	tasks := db.tasks()

	update := bson.M{}
	selector := bson.M{"id": req.Id}

	switch req.Type {
	case events.Type_TASK_CREATED:
		task := req.GetTask()
		task.Logs = []*tes.TaskLog{
			{
				Logs:       []*tes.ExecutorLog{},
				Metadata:   map[string]string{},
				SystemLogs: []string{},
			},
		}
		return db.insertTask(ctx, task)

	case events.Type_TASK_STATE:
		retrier := util.NewRetrier()
		retrier.ShouldRetry = func(err error) bool {
			_, isTransitionError := err.(*tes.TransitionError)
			return !isTransitionError && err != tes.ErrNotFound && err != tes.ErrNotPermitted
		}

		return retrier.Retry(ctx, func() error {
			// get current state & version
			state, version, err := db.findTaskStateAndVersion(ctx, req.Id)
			if err != nil {
				return err
			}

			// validate state transition
			to := req.GetState()
			if err = tes.ValidateTransition(state, to); err != nil {
				logger.Debug("mongodb: invalid state transition", "taskID", req.Id, "from", state, "to", to, "error", err)
				return err
			}

			// apply version restriction and set update
			selector["version"] = version
			update = bson.M{"$set": bson.M{"state": to, "version": time.Now().UnixNano()}}

			mctx, cancel := db.wrap(ctx)
			defer cancel()

			result, err := tasks.UpdateOne(mctx, selector, update)
			if result.MatchedCount == 0 {
				return tes.ErrConcurrentStateChange
			}

			return err
		})

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

	opts := options.UpdateOne().SetUpsert(true)
	_, err := tasks.UpdateOne(mctx, selector, update, opts)
	return err
}

func (db *MongoDB) insertTask(ctx context.Context, task *tes.Task) error {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	tasks := db.tasks()
	result, err := tasks.InsertOne(mctx, &task)

	if err == nil {
		mctx, cancel := db.wrap(ctx)
		defer cancel()

		updateOwner := bson.M{"$set": bson.M{"owner": server.GetUsername(ctx)}}
		_, err = tasks.UpdateOne(mctx, bson.M{"_id": result.InsertedID}, updateOwner)
	}

	return err
}

func (db *MongoDB) findTaskStateAndVersion(ctx context.Context, taskId string) (tes.State, interface{}, error) {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	props := make(map[string]interface{})
	opts := options.FindOne().SetProjection(bson.M{"state": 1, "version": 1, "owner": 1})
	err := db.tasks().FindOne(mctx, bson.M{"id": taskId}, opts).Decode(&props)

	if err == mongo.ErrNoDocuments {
		return tes.State_UNKNOWN, nil, tes.ErrNotFound
	} else if err != nil {
		return tes.State_UNKNOWN, nil, err
	}

	// Check if "owner" is nil
	if props["owner"] == nil {
		return tes.State_UNKNOWN, nil, fmt.Errorf("owner field is missing in task properties")
	}

	taskOwner := props["owner"].(string)
	if !server.GetUser(ctx).IsAccessible(taskOwner) {
		return tes.State_UNKNOWN, nil, tes.ErrNotPermitted
	}

	// Check if "state" is nil
	if props["state"] == nil {
		return tes.State_UNKNOWN, nil, fmt.Errorf("state field is missing in task properties")
	}

	state := tes.State(props["state"].(int32))
	version := props["version"]
	return state, version, nil
}
