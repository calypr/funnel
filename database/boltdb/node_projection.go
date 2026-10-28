package boltdb

import (
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func nodeWithoutTaskIDs(data []byte) ([]byte, bool, error) {
	projected := make([]byte, 0, 256)
	found := false
	for len(data) > 0 {
		field := data
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, false, protowire.ParseError(n)
		}
		data = data[n:]
		valueSize := protowire.ConsumeFieldValue(number, wireType, data)
		if valueSize < 0 {
			return nil, false, protowire.ParseError(valueSize)
		}
		if number == 16 {
			if wireType != protowire.BytesType {
				return nil, false, fmt.Errorf("node task IDs field has wire type %d", wireType)
			}
			found = true
		} else {
			projected = append(projected, field[:n+valueSize]...)
		}
		data = data[valueSize:]
	}
	return projected, found, nil
}

func unmarshalNodeWithoutTaskIDs(data []byte, node *scheduler.Node) error {
	projected, _, err := nodeWithoutTaskIDs(data)
	if err != nil {
		return err
	}
	if err := proto.Unmarshal(projected, node); err != nil {
		return fmt.Errorf("unmarshal projected node: %w", err)
	}
	return nil
}

func migrateNodeTaskIDProjection(tx *bolt.Tx) error {
	const (
		migrationBucket = "scheduler-migrations"
		migrationKey    = "node-taskids-projection-v1"
	)
	migrations, err := tx.CreateBucketIfNotExists([]byte(migrationBucket))
	if err != nil {
		return err
	}
	if migrations.Get([]byte(migrationKey)) != nil {
		return nil
	}
	var updates []struct {
		id   []byte
		data []byte
	}
	if err := tx.Bucket(Nodes).ForEach(func(id, data []byte) error {
		projected, found, err := nodeWithoutTaskIDs(data)
		if err != nil {
			return err
		}
		if found {
			updates = append(updates, struct {
				id   []byte
				data []byte
			}{append([]byte(nil), id...), projected})
		}
		return nil
	}); err != nil {
		return err
	}
	for _, update := range updates {
		if err := tx.Bucket(Nodes).Put(update.id, update.data); err != nil {
			return err
		}
	}
	return migrations.Put([]byte(migrationKey), []byte{1})
}

func taskResourceProjection(data []byte) (*tes.Resources, error) {
	projected, err := taskWireProjection(data, 9)
	if err != nil {
		return nil, err
	}
	task := &tes.Task{}
	if err := proto.Unmarshal(projected, task); err != nil {
		return nil, fmt.Errorf("unmarshal task resources: %w", err)
	}
	return task.Resources, nil
}

func loadNodeTaskIDs(tx *bolt.Tx, id string) ([]string, error) {
	claims := tx.Bucket(NodeClaims).Bucket([]byte(id))
	if claims == nil {
		return nil, nil
	}
	ids := make([]string, 0, claims.Stats().KeyN)
	if err := claims.ForEach(func(taskID, _ []byte) error {
		ids = append(ids, string(taskID))
		return nil
	}); err != nil {
		return nil, err
	}
	return ids, nil
}

func hasActiveNodeClaims(tx *bolt.Tx, id string) bool {
	claims := tx.Bucket(NodeClaims).Bucket([]byte(id))
	if claims == nil {
		return false
	}
	key, _ := claims.Cursor().First()
	return key != nil
}

func hydrateNodeProjection(tx *bolt.Tx, node *scheduler.Node, includeTaskIDs bool) error {
	usage, err := readNodeUsage(tx, node.Id)
	if err != nil {
		return err
	}
	version, err := readNodeVersion(tx, node.Id)
	if err != nil {
		return err
	}
	if version != 0 {
		node.Version = version
	}
	if includeTaskIDs {
		ids, err := loadNodeTaskIDs(tx, node.Id)
		if err != nil {
			return err
		}
		node.TaskIds = ids
	} else {
		node.TaskIds = nil
	}
	node.Available = usage.Available(node.Resources)
	node.LastPing = loadHeartbeat(tx, node.Id)
	return nil
}
