package boltdb

import (
	"encoding/json"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

var NodeUsage = []byte("node-resource-usage")

type nodeBudget struct {
	scheduler.ResourceUsage
	Version int64 `json:"version,omitempty"`
}

func readNodeUsage(tx *bolt.Tx, id string) (scheduler.ResourceUsage, error) {
	var budget nodeBudget
	if data := tx.Bucket(NodeUsage).Get([]byte(id)); data != nil {
		if err := json.Unmarshal(data, &budget); err != nil {
			return budget.ResourceUsage, err
		}
	}
	return budget.ResourceUsage, nil
}

func readNodeVersion(tx *bolt.Tx, id string) (int64, error) {
	var budget nodeBudget
	if data := tx.Bucket(NodeUsage).Get([]byte(id)); data != nil {
		if err := json.Unmarshal(data, &budget); err != nil {
			return 0, err
		}
	}
	return budget.Version, nil
}

func saveNodeUsageVersion(tx *bolt.Tx, id string, usage scheduler.ResourceUsage, version int64) error {
	data, err := json.Marshal(nodeBudget{ResourceUsage: usage, Version: version})
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
		return saveNodeUsageVersion(tx, string(owner), total, 0)
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
		if err := unmarshalNodeWithoutTaskIDs(data, &node); err != nil {
			return err
		}
		node.TaskIds = nil
		usage, err := readNodeUsage(tx, node.Id)
		if err != nil {
			return err
		}
		node.Available = usage.Available(node.Resources)
		node.Version++
		if err := saveNodeUsageVersion(tx, node.Id, usage, node.Version); err != nil {
			return err
		}
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
	if data := tx.Bucket(Nodes).Get(owner); data != nil {
		var node scheduler.Node
		if err := unmarshalNodeWithoutTaskIDs(data, &node); err != nil {
			return err
		}
		version, err := readNodeVersion(tx, node.Id)
		if err != nil {
			return err
		}
		if version == 0 {
			version = node.Version
		}
		return saveNodeUsageVersion(tx, string(owner), used, version+1)
	}
	return saveNodeUsageVersion(tx, string(owner), used, 0)
}
