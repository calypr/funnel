package boltdb

import (
	"context"
	"fmt"
	"slices"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// TryAssign atomically claims a queued task and records it on an eligible node.
// The task state is intentionally unchanged; the ordinary event writer remains
// responsible for moving it out of QUEUED.
func (taskBolt *BoltDB) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	if task == nil || task.GetId() == "" {
		return "", fmt.Errorf("task ID is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if candidate == nil {
		var owner string
		err := taskBolt.db.View(func(tx *bolt.Tx) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if getTaskState(tx, task.Id) != tes.State_QUEUED {
				return nil
			}
			var err error
			owner, err = activeTaskClaim(tx, task.Id)
			return err
		})
		return owner, err
	}

	var owner string
	err := taskBolt.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		tasks := tx.Bucket(TaskBucket)
		states := tx.Bucket(TaskState)
		claims := tx.Bucket(TaskClaims)
		nodes := tx.Bucket(Nodes)
		if tasks == nil || states == nil || claims == nil || nodes == nil {
			return fmt.Errorf("required task assignment bucket is missing")
		}

		key := []byte(task.Id)
		taskData := tasks.Get(key)
		if taskData == nil || string(states.Get(key)) != tes.State_QUEUED.String() {
			return nil
		}

		// Use the task persisted in the same transaction as the state check so
		// capacity is charged using the database's authoritative request.
		queuedTask := &tes.Task{}
		if err := proto.Unmarshal(taskData, queuedTask); err != nil {
			return fmt.Errorf("unmarshal queued task %q: %w", task.Id, err)
		}
		queuedTask.State = tes.State_QUEUED

		if claim := claims.Get(key); claim != nil {
			var err error
			owner, err = activeTaskClaim(tx, task.Id)
			return err
		}

		if candidate.GetId() == "" {
			return fmt.Errorf("candidate node ID is required")
		}

		data := nodes.Get([]byte(candidate.Id))
		if data == nil {
			return fmt.Errorf("candidate node %q does not exist", candidate.Id)
		}
		current := &scheduler.Node{}
		if err := proto.Unmarshal(data, current); err != nil {
			return fmt.Errorf("unmarshal candidate node %q: %w", candidate.Id, err)
		}
		if slices.Contains(current.TaskIds, task.Id) {
			return fmt.Errorf("node reservation has no task claim")
		}
		used, err := readNodeUsage(tx, current.Id)
		if err != nil {
			return err
		}
		current.Available = used.Available(current.Resources)
		if !scheduler.Match(current, queuedTask, scheduler.DefaultPredicates) {
			return fmt.Errorf("task no longer fits node %q", current.Id)
		}
		reserved, err := scheduler.TaskResourceUsage(queuedTask)
		if err != nil {
			return err
		}
		used, err = used.Add(reserved)
		if err != nil {
			return err
		}
		if err := saveNodeUsage(tx, current.Id, used); err != nil {
			return err
		}
		current.TaskIds = append(current.TaskIds, task.Id)
		current.Available = used.Available(current.Resources)
		current.Version++

		updated, err := proto.Marshal(current)
		if err != nil {
			return fmt.Errorf("marshal candidate node %q: %w", candidate.Id, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := nodes.Put([]byte(candidate.Id), updated); err != nil {
			return err
		}
		if err := putTaskClaim(tx, task.Id, candidate.Id); err != nil {
			return err
		}
		owner = candidate.Id
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("assign task %q: %w", task.Id, err)
	}
	return owner, nil
}

func activeTaskClaim(tx *bolt.Tx, id string) (string, error) {
	owner := tx.Bucket(TaskClaims).Get([]byte(id))
	if owner == nil {
		return "", nil
	}
	if len(owner) == 0 {
		return "", fmt.Errorf("task %q has an empty claim", id)
	}
	allocations := tx.Bucket(NodeClaims).Bucket(owner)
	if allocations == nil || allocations.Get([]byte(id)) == nil {
		return "", fmt.Errorf("task %q has an inactive allocation", id)
	}
	return string(owner), nil
}
