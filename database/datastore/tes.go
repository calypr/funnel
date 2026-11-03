package datastore

import (
	"context"

	"cloud.google.com/go/datastore"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"google.golang.org/api/iterator"
)

// GetTask implements the TES GetTask interface.
func (d *Datastore) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	key := taskKey(req.Id)
	entity := &task{}

	err := d.client.Get(ctx, key, entity)
	if err == datastore.ErrNoSuchEntity {
		return nil, tes.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !server.GetUser(ctx).IsAccessible(entity.Owner) {
		return nil, tes.ErrNotPermitted
	}

	task := entity.unmarshal()

	switch req.View {
	case tes.View_MINIMAL.String():
		task = task.GetMinimalView()
	case tes.View_FULL.String():
		// Determine the keys needed to load the various parts of the full view.
		parts := viewPartKeys(task)
		err := d.getFullView(ctx, parts, map[string]*tes.Task{
			task.Id: task,
		})
		if err != nil {
			return nil, err
		}
	}

	return task, nil
}

// getFullView retrieve the various parts of the full view from the database
// and unmarshals those into their respective tasks. This handles unmarshaling
// multiple tasks in one call, in order to support ListTasks. The "tasks" arg
// is a map of task ID -> task.
func (d *Datastore) getFullView(ctx context.Context, keys []*datastore.Key, tasks map[string]*tes.Task) error {
	proplists := make([]datastore.PropertyList, len(keys))
	err := d.client.GetMulti(ctx, keys, proplists)
	merr, isMerr := err.(datastore.MultiError)
	if err != nil && !isMerr {
		return err
	}

	for i, props := range proplists {
		if merr != nil && merr[i] != nil {
			// The task doesn't have this part, e.g. it doesn't have stdout for an executor.
			// That's ok, skip this part.
			continue
		}
		id := keys[i].Parent.Name
		task := tasks[id]
		if err := unmarshalPart(task, props); err != nil {
			return err
		}
	}
	return nil
}

// ListTasks implements the TES ListTasks interface.
func (d *Datastore) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {

	page := req.PageToken
	size := tes.GetPageSize(req.GetPageSize())
	q := datastore.NewQuery("Task").Limit(size).Order("-CreationTime")

	if page != "" {
		c, err := datastore.DecodeCursor(page)
		if err != nil {
			return nil, err
		}
		q = q.Start(c)
	}

	if userInfo := server.GetUser(ctx); !userInfo.CanSeeAllTasks() {
		q = q.FilterField("Owner", "=", userInfo.Username)
	}

	if req.State != tes.Unknown {
		q = q.FilterField("State", "=", int32(req.State))
	}

	for k, v := range req.GetTags() {
		q = q.FilterField("TagStrings", "=", encodeKV(k, v))
	}

	pageResult := tes.NewTaskPage(ctx)
	hasMore := false
	accepted := 0

	it := d.client.Run(ctx, q)
	for {
		t := &task{}
		_, err := it.Next(t)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		task := t.unmarshal()

		// Materialize FULL before checking the response budget. Otherwise large
		// output logs would evade the limit and fail later during gRPC encoding.
		switch req.View {
		case tes.View_MINIMAL.String():
			task = task.GetMinimalView()
		case tes.View_FULL.String():
			if err := d.getFullView(ctx, viewPartKeys(task), map[string]*tes.Task{task.Id: task}); err != nil {
				return nil, err
			}
		}
		cursor, err := it.Cursor()
		if err != nil {
			return nil, err
		}
		candidateToken := cursor.String()
		added, err := pageResult.Add(task, candidateToken)
		if err != nil {
			return nil, err
		}
		if !added {
			hasMore = true
			break
		}
		accepted++
	}
	if !hasMore {
		hasMore = accepted == size
	}
	return pageResult.Finish(hasMore), nil
}
