package mongodb

import (
	"fmt"

	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/net/context"
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
		opts = opts.SetProjection(minimalView)
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	result := db.tasks().FindOne(mctx, bson.M{"id": req.Id}, opts)
	if result.Err() != nil {
		return nil, tes.ErrNotFound
	}

	var owner TaskOwner
	_ = result.Decode(&owner)
	if !server.GetUser(ctx).IsAccessible(owner.Owner) {
		return nil, tes.ErrNotPermitted
	}

	var task tes.Task
	if err := result.Decode(&task); err != nil {
		return nil, tes.ErrNotFound
	}
	return &task, nil
}

// ListTasks returns a list of taskIDs
func (db *MongoDB) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	pageSize := tes.GetPageSize(req.GetPageSize())

	var query = bson.M{}
	var err error
	if req.PageToken != "" {
		query["id"] = bson.M{"$lt": req.PageToken}
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

	var opts = options.Find().SetSort(bson.M{"creationtime": -1}).SetLimit(int64(pageSize))

	switch req.View {
	case tes.View_BASIC.String():
		opts = opts.SetProjection(basicView)
	case tes.View_MINIMAL.String():
		opts = opts.SetProjection(minimalView)
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	cursor, err := db.tasks().Find(mctx, query, opts)
	if err != nil {
		return nil, err
	}

	mctx, cancel = db.wrap(ctx)
	defer cancel()

	var tasks []*tes.Task
	err = cursor.All(mctx, &tasks)
	if err != nil {
		return nil, err
	}

	out := tes.ListTasksResponse{
		Tasks: tasks,
	}
	if len(tasks) == pageSize {
		out.NextPageToken = tasks[len(tasks)-1].Id
	}

	return &out, nil
}
