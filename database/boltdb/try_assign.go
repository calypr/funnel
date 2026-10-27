package boltdb

import (
	"context"
	"fmt"
	"slices"
	"time"

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

		assignedOwner, err := findTaskAssignment(nodes, task.Id)
		if err != nil {
			return err
		}

		if claim := claims.Get(key); claim != nil {
			owner = string(claim)
			if owner == "" {
				return fmt.Errorf("task %q has an empty persisted node claim", task.Id)
			}
			if assignedOwner == owner {
				return nil
			}
			if assignedOwner != "" {
				return fmt.Errorf("task %q claim names node %q but node %q already contains it", task.Id, owner, assignedOwner)
			}
			if nodes.Get([]byte(owner)) == nil {
				// Return the durable owner so the scheduler can report a missing node.
				return nil
			}
			return fmt.Errorf("task %q has persisted claim for node %q without a node assignment", task.Id, owner)
		}

		if assignedOwner != "" {
			// Adopt an assignment written by the pre-claim scheduler. It already
			// consumes node capacity, so only the missing claim is persisted.
			owner = assignedOwner
			if candidate == nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return claims.Put(key, []byte(owner))
		}

		if candidate == nil {
			return nil
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
		if err := appendTaskToNode(current, queuedTask); err != nil {
			return fmt.Errorf("assign task %q to node %q: %w", task.Id, candidate.Id, err)
		}

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
		if err := claims.Put(key, []byte(candidate.Id)); err != nil {
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

// findTaskAssignment returns the unique node containing taskID, if any.
func findTaskAssignment(nodes *bolt.Bucket, taskID string) (string, error) {
	var owner string
	count := 0
	err := nodes.ForEach(func(k, v []byte) error {
		if v == nil {
			return nil
		}
		node := &scheduler.Node{}
		if err := proto.Unmarshal(v, node); err != nil {
			return fmt.Errorf("unmarshal node %q while checking task assignment: %w", string(k), err)
		}
		if slices.Contains(node.TaskIds, taskID) {
			count++
			owner = node.Id
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if count > 1 {
		return "", fmt.Errorf("task %q is assigned to multiple nodes", taskID)
	}
	return owner, nil
}

func appendTaskToNode(node *scheduler.Node, task *tes.Task) error {
	for _, id := range node.TaskIds {
		if id == task.Id {
			return nil
		}
	}
	if !scheduler.Match(node, task, scheduler.DefaultPredicates) {
		return fmt.Errorf("task does not fit node resources or predicates")
	}
	node.TaskIds = append(node.TaskIds, task.Id)
	node.Available = scheduler.SubtractResources(task, node.Available)
	// Updating a node through PutNode refreshes LastPing and increments Version.
	// Keep the same observable node bookkeeping for the atomic assignment path.
	node.LastPing = time.Now().UnixNano()
	node.Version++
	return nil
}
