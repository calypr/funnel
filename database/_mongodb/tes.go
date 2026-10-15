package mongodb

import (
	"fmt"

	"context"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/ohsu-comp-bio/funnel/tes"
)

var basicView = bson.M{
	"logs.systemlogs":  0,
	"logs.logs.stdout": 0,
	"logs.logs.stderr": 0,
	"inputs.content":   0,
}
var minimalView = bson.M{"id": 1, "state": 1}

// GetTask gets a task, which describes a running task
func (db *MongoDB) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	sess := db.sess.Copy()
	defer sess.Close()

	var task tes.Task
	var q *mgo.Query

	q = db.tasks(sess).Find(bson.M{"id": req.Id})
	switch req.View {
	case tes.View_BASIC.String():
		q = q.Select(basicView)
	case tes.View_MINIMAL.String():
		q = q.Select(minimalView)
	}

	err := q.One(&task)
	if err == mgo.ErrNotFound {
		return nil, tes.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &task, nil
}

// ListTasks returns a list of taskIDs
func (db *MongoDB) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	sess := db.sess.Copy()
	defer sess.Close()

	pageSize := tes.GetPageSize(req.GetPageSize())

	var query = bson.M{}
	var q *mgo.Query
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

	for k, v := range req.GetTags() {
		if v == "" {
			query[fmt.Sprintf("tags.%s", k)] = bson.M{"$exists": true}
		} else {
			query[fmt.Sprintf("tags.%s", k)] = bson.M{"$eq": v}
		}
	}

	q = db.tasks(sess).Find(query).Sort("-creationtime").Limit(pageSize)

	switch req.View {
	case tes.View_BASIC.String():
		q = q.Select(basicView)
	case tes.View_MINIMAL.String():
		q = q.Select(minimalView)
	}

	var tasks []*tes.Task
	err = q.All(&tasks)
	if err != nil {
		return nil, err
	}

	out := tes.ListTasksResponse{
		Tasks: tasks,
	}
	// TODO figure out when not to return a next page token
	if len(tasks) > 0 {
		out.NextPageToken = tasks[len(tasks)-1].Id
	}

	return &out, nil
}
