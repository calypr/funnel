package boltdb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queueTask adds a task to the scheduler queue.
func (taskBolt *BoltDB) queueTask(task *tes.Task) error {
	taskID := task.Id
	idBytes := []byte(taskID)

	err := taskBolt.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(TasksQueued).Put(idBytes, []byte{})
	})
	if err != nil {
		return fmt.Errorf("can't queue task: %s", err)
	}
	return nil
}

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (taskBolt *BoltDB) ReadQueue(n int, after *tes.Task) []*tes.Task {
	tasks := make([]*tes.Task, 0)
	if err := taskBolt.db.View(func(tx *bolt.Tx) error {
		// Iterate over the TasksQueued bucket, reading the first `n` tasks
		c := tx.Bucket(TasksQueued).Cursor()
		k, _ := c.First()
		if after != nil {
			k, _ = c.Seek([]byte(after.Id))
			if k != nil && string(k) == after.Id {
				k, _ = c.Next()
			}
		}
		for ; k != nil && len(tasks) < n; k, _ = c.Next() {
			id := string(k)
			task, err := readQueuedTaskProjection(tx, id)
			if err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return nil
	}); err != nil {
		taskBolt.log.Error("reading task queue", "error", err)
	}
	return tasks
}

// PutNode put a node object into the database.
//
// For optimisic locking, if the node already exists and node.Version
// doesn't match the version in the database, an error is returned.
func (taskBolt *BoltDB) PutNode(ctx context.Context, node *scheduler.Node) (*scheduler.PutNodeResponse, error) {
	err := taskBolt.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return putReportedNode(tx, node)
	})
	return &scheduler.PutNodeResponse{}, err
}

// GetNode gets a node
func (taskBolt *BoltDB) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	var node *scheduler.Node

	err := taskBolt.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(Nodes).Get([]byte(req.Id))
		if data == nil {
			return errNotFound
		}

		node = &scheduler.Node{}
		if err := unmarshalNodeWithoutTaskIDs(data, node); err != nil {
			return err
		}
		return hydrateNodeProjection(tx, node, true)
	})

	if err == errNotFound {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err.Error(), req.Id)
	}

	if err != nil {
		return nil, err
	}
	return node, nil
}

// DeleteNode deletes the given node.
// Currently, the node's version field is not checked.
func (taskBolt *BoltDB) DeleteNode(ctx context.Context, node *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	// TODO we don't check version on delete. should we?
	return &scheduler.DeleteNodeResponse{}, taskBolt.db.Update(func(tx *bolt.Tx) error {
		if node.Version != 0 {
			data := tx.Bucket(Nodes).Get([]byte(node.Id))
			if data == nil {
				return scheduler.ErrNodeChanged
			}
			var current scheduler.Node
			if err := unmarshalNodeWithoutTaskIDs(data, &current); err != nil {
				return err
			}
			if version, err := readNodeVersion(tx, current.Id); err != nil {
				return err
			} else if version != 0 {
				current.Version = version
			}
			finishedCleanup := node.State == scheduler.NodeState_GONE && current.State == scheduler.NodeState_GONE && !hasActiveNodeClaims(tx, node.Id)
			if (current.Version != node.Version && !finishedCleanup) || loadHeartbeat(tx, node.Id) != node.LastPing {
				return scheduler.ErrNodeChanged
			}
		}
		if err := tx.Bucket(NodeHeartbeats).Delete([]byte(node.Id)); err != nil {
			return err
		}
		return tx.Bucket(Nodes).Delete([]byte(node.Id))
	})
}

// ListNodes is an API endpoint that returns a list of nodes.
func (taskBolt *BoltDB) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return taskBolt.listNodes(ctx, req, true)
}

// ListNodeCandidates returns scheduling data without expanding each node's active task IDs.
func (taskBolt *BoltDB) ListNodeCandidates(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return taskBolt.listNodes(ctx, req, false)
}

func (taskBolt *BoltDB) listNodes(ctx context.Context, req *scheduler.ListNodesRequest, includeTaskIDs bool) (*scheduler.ListNodesResponse, error) {
	page, err := scheduler.ParseNodePage(req)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var nodes []*scheduler.Node

	err = taskBolt.db.View(func(tx *bolt.Tx) error {

		bucket := tx.Bucket(Nodes)
		c := bucket.Cursor()
		var k, v []byte
		if page.AfterID == "" {
			k, v = c.First()
		} else {
			k, v = c.Seek([]byte(page.AfterID))
			if string(k) == page.AfterID {
				k, v = c.Next()
			}
		}
		for ; k != nil && len(nodes) < page.Limit+1; k, v = c.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			node := &scheduler.Node{}
			err := unmarshalNodeWithoutTaskIDs(v, node)
			if err != nil {
				return err
			}
			if err := hydrateNodeProjection(tx, node, includeTaskIDs); err != nil {
				return err
			}
			nodes = append(nodes, node)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
