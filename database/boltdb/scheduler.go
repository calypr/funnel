package boltdb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
func (taskBolt *BoltDB) ReadQueue(n int) []*tes.Task {
	tasks := make([]*tes.Task, 0)
	if err := taskBolt.db.View(func(tx *bolt.Tx) error {
		// Iterate over the TasksQueued bucket, reading the first `n` tasks
		c := tx.Bucket(TasksQueued).Cursor()
		for k, _ := c.First(); k != nil && len(tasks) < n; k, _ = c.Next() {
			id := string(k)
			task, err := getTaskView(tx, id, tes.View_FULL, nil)
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

		existing := &scheduler.Node{}
		data := tx.Bucket(Nodes).Get([]byte(node.Id))
		if data != nil {
			if err := proto.Unmarshal(data, existing); err != nil {
				return fmt.Errorf("unmarshaling existing node %s: %w", node.Id, err)
			}
		}

		if existing.GetVersion() != 0 && node.Version != existing.GetVersion() {
			return fmt.Errorf("Version outdated")
		}

		err := scheduler.UpdateNode(ctx, taskBolt, node, existing)
		if err != nil {
			return err
		}

		data, err = proto.Marshal(node)
		if err != nil {
			return err
		}
		return tx.Bucket(Nodes).Put([]byte(node.Id), data)
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
		return proto.Unmarshal(data, node)
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
		return tx.Bucket(Nodes).Delete([]byte(node.Id))
	})
}

// ListNodes is an API endpoint that returns a list of nodes.
func (taskBolt *BoltDB) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	resp := &scheduler.ListNodesResponse{}

	err := taskBolt.db.View(func(tx *bolt.Tx) error {

		bucket := tx.Bucket(Nodes)
		c := bucket.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			node := &scheduler.Node{}
			err := proto.Unmarshal(v, node)
			if err != nil {
				return err
			}
			resp.Nodes = append(resp.Nodes, node)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return resp, nil
}
