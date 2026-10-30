package badger

import (
	"bytes"
	"context"
	"fmt"

	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	badger "github.com/dgraph-io/badger/v4"
	"google.golang.org/protobuf/proto"
)

// GetTask gets a task, which describes a running task
func (db *Badger) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	var task *tes.Task

	err := db.db.View(func(txn *badger.Txn) error {
		t, err := getTask(txn, req.Id, ctx)
		task = t
		return err
	})
	if err != nil {
		return nil, err
	}

	switch req.View {
	case tes.View_MINIMAL.String():
		task = task.GetMinimalView()
	case tes.View_BASIC.String():
		task = task.GetBasicView()
	}
	return task, nil
}

// ListTasks returns a list of tasks.
func (db *Badger) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	page := tes.NewTaskPage(ctx)
	pageSize := tes.GetPageSize(req.GetPageSize())
	count := 0
	byteLimited := false

	err := db.db.View(func(txn *badger.Txn) error {

		it := txn.NewIterator(badger.IteratorOptions{
			// Keys (task IDs) are in ascending order, and we want the first page
			// to be the most recent task, so that's at the end of the list.
			Reverse:        true,
			PrefetchValues: true,
			PrefetchSize:   pageSize,
		})
		defer it.Close()

		i := 0

		// For pagination, figure out the starting key.
		if req.PageToken != "" {
			it.Seek(taskKey(req.PageToken))
			// Seek moves to the key, but the start of the page is the next key.
			it.Next()
		} else {
			it.Rewind()
		}

	taskLoop:
		for ; it.Valid() && count < pageSize; it.Next() {
			// Iterator items are reverse-ordered by keys (starting with
			// task-keys). So by the time the task-key prefix is passed, only
			// owner-keys remains, and they can be skipped.
			if !bytes.HasPrefix(it.Item().Key(), taskKeyPrefix) {
				break
			}

			taskOwner := getTaskOwner(txn, ownerKeyFromTaskKey(it.Item().Key()))
			if !isAccessible(ctx, taskOwner) {
				continue
			}

			var val []byte
			err := it.Item().Value(func(d []byte) error {
				val = copyBytes(d)
				return nil
			})
			if err != nil {
				return fmt.Errorf("loading item value: %s", err)
			}

			// Load task.
			task := &tes.Task{}
			err = proto.Unmarshal(val, task)
			if err != nil {
				return fmt.Errorf("unmarshaling data: %s", err)
			}

			// Filter tasks by tag.
			for k, v := range req.GetTags() {
				tval, ok := task.Tags[k]
				if !ok || tval != v {
					continue taskLoop
				}
			}

			// Filter tasks by state.
			if req.State != tes.Unknown && req.State != task.State {
				continue taskLoop
			}

			switch req.View {
			case tes.View_MINIMAL.String():
				task = task.GetMinimalView()
			case tes.View_BASIC.String():
				task = task.GetBasicView()
			}

			accepted, err := page.Add(task, task.Id)
			if err != nil {
				return err
			}
			if !accepted {
				byteLimited = true
				break taskLoop
			}
			count++
			i++
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return page.Finish(byteLimited || count == pageSize), nil
}

func getTask(txn *badger.Txn, id string, ctx context.Context) (*tes.Task, error) {
	item, err := txn.Get(taskKey(id))
	if err == badger.ErrKeyNotFound {
		return nil, tes.ErrNotFound
	}
	if !isAccessible(ctx, getTaskOwner(txn, ownerKey(id))) {
		return nil, tes.ErrNotPermitted
	}
	if err != nil {
		return nil, fmt.Errorf("loading item: %s", err)
	}

	var val []byte
	err = item.Value(func(d []byte) error {
		val = copyBytes(d)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("loading item value: %s", err)
	}

	task := &tes.Task{}
	err = proto.Unmarshal(val, task)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling data: %s", err)
	}
	return task, nil
}

func getTaskOwner(txn *badger.Txn, ownerKey []byte) string {
	taskOwner := ""
	if item, err := txn.Get(ownerKey); err == nil {
		_ = item.Value(func(d []byte) error {
			taskOwner = string(d)
			return nil
		})
	}
	return taskOwner
}

func isAccessible(ctx context.Context, taskOwner string) bool {
	return server.GetUser(ctx).IsAccessible(taskOwner)
}

func copyBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
