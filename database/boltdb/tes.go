package boltdb

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/proto"
)

func getTaskState(tx *bolt.Tx, id string) tes.State {
	idBytes := []byte(id)
	s := tx.Bucket(TaskState).Get(idBytes)
	if s == nil {
		return tes.State_UNKNOWN
	}
	// map the string into the protobuf enum
	v := tes.State_value[string(s)]
	return tes.State(v)
}

func loadTask(tx *bolt.Tx, id string, task *tes.Task, ctx context.Context) error {
	b := tx.Bucket(TaskBucket).Get([]byte(id))
	if b == nil {
		return tes.ErrNotFound
	}

	if err := checkOwner(tx, id, ctx); err != nil {
		return err
	}

	if task != nil {
		proto.Unmarshal(b, task)
		task.State = getTaskState(tx, id)
	}

	return nil
}

func loadMinimalTaskView(tx *bolt.Tx, id string, task *tes.Task, ctx context.Context) error {
	if err := loadTask(tx, id, nil, ctx); err != nil {
		return err
	}
	task.Id = id
	task.State = getTaskState(tx, id)
	return nil
}

func loadBasicTaskView(tx *bolt.Tx, id string, task *tes.Task, ctx context.Context) error {
	err := loadTask(tx, id, task, ctx)
	if err != nil {
		return err
	}

	loadTaskLogs(tx, task)

	// remove content from inputs
	for _, v := range task.Inputs {
		v.Content = ""
	}

	return nil
}

func loadFullTaskView(tx *bolt.Tx, id string, task *tes.Task, ctx context.Context) error {
	err := loadTask(tx, id, task, ctx)
	if err != nil {
		return err
	}
	loadTaskLogs(tx, task)

	// Load executor stdout/err
	for _, tl := range task.Logs {
		for j, el := range tl.Logs {
			key := []byte(fmt.Sprint(id, j))

			if b := tx.Bucket(ExecutorStdout).Get(key); b != nil {
				el.Stdout = string(b)
			}

			if b := tx.Bucket(ExecutorStderr).Get(key); b != nil {
				el.Stderr = string(b)
			}
		}
	}

	// Load system logs
	var syslogs []string
	slb := tx.Bucket(SysLogs).Get([]byte(id))
	if slb != nil {
		err := json.Unmarshal(slb, &syslogs)
		if err != nil {
			return err
		}
		task.Logs[0].SystemLogs = syslogs
	}

	return nil
}

func loadTaskLogs(tx *bolt.Tx, task *tes.Task) {
	tasklog := &tes.TaskLog{}
	task.Logs = []*tes.TaskLog{tasklog}

	b := tx.Bucket(TasksLog).Get([]byte(task.Id))
	if b != nil {
		proto.Unmarshal(b, tasklog)
	}

	for i := range task.Executors {
		o := tx.Bucket(ExecutorLogs).Get([]byte(fmt.Sprint(task.Id, i)))
		if o != nil {
			var execlog tes.ExecutorLog
			proto.Unmarshal(o, &execlog)
			tasklog.Logs = append(tasklog.Logs, &execlog)
		}
	}
}

// GetTask gets a task, which describes a running task
func (taskBolt *BoltDB) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	var task *tes.Task
	var err error

	err = taskBolt.db.View(func(tx *bolt.Tx) error {
		if req.View == "" {
			req.View = tes.View_MINIMAL.String()
		}
		_, ok := tes.View_value[req.View]
		if !ok {
			return fmt.Errorf("Unknown view: %s", req.View)
		}
		task, err = getTaskView(tx, req.Id, tes.View(tes.View_value[req.View]), ctx)
		return err
	})
	return task, err
}

func getTaskView(tx *bolt.Tx, id string, view tes.View, ctx context.Context) (*tes.Task, error) {
	var err error

	task := &tes.Task{}

	switch {
	case view == tes.View_MINIMAL:
		err = loadMinimalTaskView(tx, id, task, ctx)
	case view == tes.View_BASIC:
		err = loadBasicTaskView(tx, id, task, ctx)
	case view == tes.View_FULL:
		err = loadFullTaskView(tx, id, task, ctx)
	default:
		err = fmt.Errorf("Unknown view: %s", view.String())
	}
	return task, err
}

// ListTasks returns a list of taskIDs
func (taskBolt *BoltDB) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	var tasks []*tes.Task
	// If the tags filter request is non-nil we need the basic or full view
	view := req.View
	if req.View == tes.Minimal.String() && req.GetTags() != nil {
		view = tes.View_BASIC.String()
	}
	viewMode := tes.View(tes.View_value[view])
	pageSize := tes.GetPageSize(req.GetPageSize())

	taskBolt.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(TaskBucket).Cursor()

		i := 0

		// For pagination, figure out the starting key.
		var k []byte
		if req.PageToken != "" {
			// Seek moves to the key, but the start of the page is the next key.
			c.Seek([]byte(req.PageToken))
			k, _ = c.Prev()
		} else {
			// No pagination, so take the last key.
			// Keys (task IDs) are in ascending order, and we want the first page
			// to be the most recent task, so that's at the end of the list.
			k, _ = c.Last()
		}

	taskLoop:
		for ; k != nil && i < pageSize; k, _ = c.Prev() {
			taskId := string(k)

			task, err := getTaskView(tx, taskId, tes.View_BASIC, ctx)
			if err != nil {
				continue taskLoop // Skip the task as access to it was not confirmed
			}

			if req.State != tes.Unknown && req.State != task.State {
				continue taskLoop
			}

			if !strings.HasPrefix(task.Name, req.NamePrefix) {
				continue taskLoop
			}

			for k, v := range req.GetTags() {
				tval, ok := task.Tags[k]
				if !ok || (tval != v && v != "") {
					continue taskLoop
				}
			}

			if viewMode != tes.View_BASIC {
				task, _ = getTaskView(tx, taskId, viewMode, ctx)
			}

			tasks = append(tasks, task)
			i++
		}
		return nil
	})

	out := tes.ListTasksResponse{
		Tasks: tasks,
	}

	if len(tasks) == pageSize {
		out.NextPageToken = &tasks[len(tasks)-1].Id
	}

	return &out, nil
}

func checkOwner(tx *bolt.Tx, taskId string, ctx context.Context) error {
	// Skip access-check for system-related operations where ctx is undefined:
	if ctx == nil || server.GetUser(ctx).CanSeeAllTasks() {
		return nil
	}

	taskOwner := ""
	if owner := tx.Bucket(TaskOwner).Get([]byte(taskId)); owner != nil {
		taskOwner = string(owner)
	}

	if server.GetUser(ctx).IsAccessible(taskOwner) {
		return nil
	}
	return tes.ErrNotPermitted
}
