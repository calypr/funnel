package mongodb

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var basicView = bson.M{
	"logs.systemlogs":  0,
	"logs.logs.stdout": 0,
	"logs.logs.stderr": 0,
	"inputs.content":   0,
}
var minimalView = bson.M{"id": 1, "owner": 1, "state": 1}

type TaskOwner struct {
	Owner string `bson:"owner"`
}

// GetTask gets a task, which describes a running task
func (db *MongoDB) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	var opts = options.FindOne()

	switch req.View {
	case tes.View_BASIC.String():
		opts = opts.SetProjection(basicView)
	case tes.View_MINIMAL.String():
		opts = opts.SetProjection(bson.M{"id": 1, "owner": 1, "state": 1, "creationtime": 1})
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	result := db.tasks().FindOne(mctx, bson.M{"id": req.Id}, opts)
	if result.Err() != nil {
		// Only a missing document is a 404; transport and server errors
		// must surface as failures, not as missing tasks.
		if errors.Is(result.Err(), mongo.ErrNoDocuments) {
			return nil, tes.ErrNotFound
		}
		return nil, fmt.Errorf("mongo get task: %w", result.Err())
	}

	var owner TaskOwner
	if err := result.Decode(&owner); err != nil {
		// An undecodable owner must not silently pass or fail the
		// authorization check below.
		return nil, fmt.Errorf("mongo get task owner: %w", err)
	}
	if !server.GetUser(ctx).IsAccessible(owner.Owner) {
		return nil, tes.ErrNotPermitted
	}

	var task tes.Task
	if err := result.Decode(&task); err != nil {
		return nil, fmt.Errorf("mongo decode task: %w", err)
	}
	return &task, nil
}

// ListTasks returns a list of taskIDs
func (db *MongoDB) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	pageSize := tes.GetPageSize(req.GetPageSize())

	var query = bson.M{}
	var err error
	if req.PageToken != "" {
		position, legacy, decodeErr := tes.DecodeTaskCursor(req.PageToken)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if legacy {
			var anchor tes.Task
			mctx, cancel := db.wrap(ctx)
			lookupErr := db.tasks().FindOne(mctx, bson.M{"id": position.ID}, options.FindOne().SetProjection(bson.M{"creationtime": 1})).Decode(&anchor)
			cancel()
			if lookupErr == mongo.ErrNoDocuments {
				return nil, status.Error(codes.InvalidArgument, "legacy task page token no longer exists; restart listing")
			}
			if lookupErr != nil {
				return nil, lookupErr
			}
			position.Created = anchor.CreationTime
		}
		query["$or"] = bson.A{
			bson.M{"creationtime": bson.M{"$lt": position.Created}},
			bson.M{"creationtime": position.Created, "id": bson.M{"$lt": position.ID}},
		}
	}

	if req.State != tes.Unknown {
		query["state"] = bson.M{"$eq": req.State}
	}

	if req.NamePrefix != "" {
		query["name"] = bson.M{"$regex": fmt.Sprintf("^%s", req.NamePrefix)}
	}

	if userInfo := server.GetUser(ctx); !userInfo.CanSeeAllTasks() {
		query["owner"] = bson.M{"$eq": userInfo.Username}
	}

	for k, v := range req.GetTags() {
		if v == "" {
			query[fmt.Sprintf("tags.%s", k)] = bson.M{"$exists": true}
		} else {
			query[fmt.Sprintf("tags.%s", k)] = bson.M{"$eq": v}
		}
	}

	var opts = options.Find().SetSort(bson.D{{Key: "creationtime", Value: -1}, {Key: "id", Value: -1}}).SetLimit(int64(pageSize))

	switch req.View {
	case tes.View_BASIC.String():
		opts = opts.SetProjection(basicView)
	case tes.View_MINIMAL.String():
		opts = opts.SetProjection(bson.M{"id": 1, "owner": 1, "state": 1, "creationtime": 1})
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	cursor, err := db.tasks().Find(mctx, query, opts)
	if err != nil {
		return nil, err
	}

	defer cursor.Close(mctx) //nolint:errcheck // Cursor cleanup cannot change results; decode and iteration errors are returned below.

	mctx, cancel = db.wrap(ctx)
	defer cancel()

	page := tes.NewTaskPage(ctx)
	taskCount := 0
	hasMore := false
	for cursor.Next(mctx) {
		var task tes.Task
		if err := cursor.Decode(&task); err != nil {
			return nil, fmt.Errorf("mongo decode listed task: %w", err)
		}
		position := tes.EncodeTaskCursor(task.CreationTime, task.Id)
		projected := &task
		if req.View == tes.View_MINIMAL.String() {
			projected = task.GetMinimalView()
		}
		accepted, err := page.Add(projected, position)
		if err != nil {
			return nil, err
		}
		if !accepted {
			hasMore = true
			break
		}
		taskCount++
		if taskCount == pageSize {
			hasMore = true
			break
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return page.Finish(hasMore), nil
}
