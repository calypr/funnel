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

var NodeUsage = []byte("node-resource-usage")

func readNodeUsage(tx *bolt.Tx, id string) (scheduler.ResourceUsage, error) {
	var usage scheduler.ResourceUsage
	if data := tx.Bucket(NodeUsage).Get([]byte(id)); data != nil {
		if err := json.Unmarshal(data, &usage); err != nil {
			return usage, err
		}
	}
	return usage, nil
}
func saveNodeUsage(tx *bolt.Tx, id string, usage scheduler.ResourceUsage) error {
	data, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	return tx.Bucket(NodeUsage).Put([]byte(id), data)
}
func migrateResourceReservations(tx *bolt.Tx) error {
	if tx.Bucket(NodeUsage) != nil {
		return nil
	}
	if _, err := tx.CreateBucket(NodeUsage); err != nil {
		return err
	}
	if err := tx.Bucket(NodeClaims).ForEach(func(owner, _ []byte) error {
		claims := tx.Bucket(NodeClaims).Bucket(owner)
		if claims == nil {
			return fmt.Errorf("invalid node claim index %q", owner)
		}
		var total scheduler.ResourceUsage
		var updates []struct {
			id   string
			data []byte
		}
		if err := claims.ForEach(func(id, _ []byte) error {
			data := tx.Bucket(TaskBucket).Get(id)
			if data == nil {
				return fmt.Errorf("reservation references missing task %q", id)
			}
			var task tes.Task
			if err := proto.Unmarshal(data, &task); err != nil {
				return err
			}
			usage, err := scheduler.TaskResourceUsage(&task)
			if err != nil {
				return err
			}
			total, err = total.Add(usage)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(usage)
			if err != nil {
				return err
			}
			updates = append(updates, struct {
				id   string
				data []byte
			}{string(id), encoded})
			return nil
		}); err != nil {
			return err
		}
		for _, update := range updates {
			if err := claims.Put([]byte(update.id), update.data); err != nil {
				return err
			}
		}
		return saveNodeUsage(tx, string(owner), total)
	}); err != nil {
		return err
	}
	// Rewrite legacy projections once. Normal assignment/release updates one node.
	var updates []struct {
		id   string
		data []byte
	}
	if err := tx.Bucket(Nodes).ForEach(func(id, data []byte) error {
		var node scheduler.Node
		if err := proto.Unmarshal(data, &node); err != nil {
			return err
		}
		node.TaskIds = nil
		if claims := tx.Bucket(NodeClaims).Bucket(id); claims != nil {
			if err := claims.ForEach(func(taskID, _ []byte) error { node.TaskIds = append(node.TaskIds, string(taskID)); return nil }); err != nil {
				return err
			}
		}
		usage, err := readNodeUsage(tx, node.Id)
		if err != nil {
			return err
		}
		node.Available = usage.Available(node.Resources)
		node.Version++
		data, err = proto.Marshal(&node)
		if err != nil {
			return err
		}
		updates = append(updates, struct {
			id   string
			data []byte
		}{string(id), data})
		return nil
	}); err != nil {
		return err
	}
	for _, update := range updates {
		if err := tx.Bucket(Nodes).Put([]byte(update.id), update.data); err != nil {
			return err
		}
	}
	return nil
}

func releaseTaskReservation(tx *bolt.Tx, id string) error {
	owner := tx.Bucket(TaskClaims).Get([]byte(id))
	if owner == nil {
		return nil
	}
	claims := tx.Bucket(NodeClaims).Bucket(owner)
	if claims == nil || claims.Get([]byte(id)) == nil {
		return nil
	}
	var reserved scheduler.ResourceUsage
	if err := json.Unmarshal(claims.Get([]byte(id)), &reserved); err != nil {
		return err
	}
	used, err := readNodeUsage(tx, string(owner))
	if err != nil {
		return err
	}
	used, err = used.Release(reserved)
	if err != nil {
		return err
	}
	if err := claims.Delete([]byte(id)); err != nil {
		return err
	}
	if err := saveNodeUsage(tx, string(owner), used); err != nil {
		return err
	}
	if data := tx.Bucket(Nodes).Get(owner); data != nil {
		var node scheduler.Node
		if err := proto.Unmarshal(data, &node); err != nil {
			return err
		}
		node.TaskIds = slices.DeleteFunc(node.TaskIds, func(taskID string) bool { return taskID == id })
		node.Available = used.Available(node.Resources)
		node.Version++
		data, err = proto.Marshal(&node)
		if err != nil {
			return err
		}
		return tx.Bucket(Nodes).Put(owner, data)
	}
	return nil
}
