package boltdb

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func putTaskClaim(tx *bolt.Tx, taskID, nodeID string) error {
	claims := tx.Bucket(TaskClaims)
	if owner := claims.Get([]byte(taskID)); owner != nil && string(owner) != nodeID {
		return fmt.Errorf("task %q is already claimed by node %q", taskID, owner)
	}
	if err := claims.Put([]byte(taskID), []byte(nodeID)); err != nil {
		return err
	}
	node, err := tx.Bucket(NodeClaims).CreateBucketIfNotExists([]byte(nodeID))
	if err != nil {
		return err
	}
	var task tes.Task
	if err := proto.Unmarshal(tx.Bucket(TaskBucket).Get([]byte(taskID)), &task); err != nil {
		return err
	}
	usage, err := scheduler.TaskResourceUsage(&task)
	if err != nil {
		return err
	}
	data, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	return node.Put([]byte(taskID), data)
}

// Migration runs once under the same write transaction that creates the index.
// Older schedulers must be stopped before upgrading.
func migrateTaskClaims(tx *bolt.Tx) error {
	if tx.Bucket(NodeClaims) != nil {
		return nil
	}
	if _, err := tx.CreateBucket(NodeClaims); err != nil {
		return err
	}
	nodes, claims := tx.Bucket(Nodes), tx.Bucket(TaskClaims)
	if err := nodes.ForEach(func(key, data []byte) error {
		var node scheduler.Node
		if err := proto.Unmarshal(data, &node); err != nil {
			return err
		}
		if node.Id != string(key) {
			return fmt.Errorf("node key %q does not match stored ID %q", key, node.Id)
		}
		for _, id := range node.TaskIds {
			if tx.Bucket(TaskBucket).Get([]byte(id)) == nil || tes.TerminalState(getTaskState(tx, id)) {
				continue
			}
			if err := putTaskClaim(tx, id, node.Id); err != nil {
				return fmt.Errorf("migrate node assignments: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return claims.ForEach(func(taskID, owner []byte) error {
		if len(owner) == 0 {
			return fmt.Errorf("task %q has an empty claim", taskID)
		}
		if tx.Bucket(TaskBucket).Get(taskID) == nil || tes.TerminalState(getTaskState(tx, string(taskID))) {
			return nil
		}
		if data := nodes.Get(owner); data != nil {
			var node scheduler.Node
			if err := proto.Unmarshal(data, &node); err != nil {
				return err
			}
			if !slices.Contains(node.TaskIds, string(taskID)) {
				return fmt.Errorf("task %q has a claim for node %q without its reservation; resolve before upgrading", taskID, owner)
			}
		}
		bucket, err := tx.Bucket(NodeClaims).CreateBucketIfNotExists(owner)
		if err != nil {
			return err
		}
		return bucket.Put(taskID, []byte{})
	})
}
