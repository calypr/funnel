package boltdb

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

var NodeHeartbeats = []byte("node-heartbeats")

func saveHeartbeat(tx *bolt.Tx, id string, stamp int64) error {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], uint64(stamp))
	return tx.Bucket(NodeHeartbeats).Put([]byte(id), data[:])
}
func loadHeartbeat(tx *bolt.Tx, id string) int64 {
	data := tx.Bucket(NodeHeartbeats).Get([]byte(id))
	if len(data) != 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(data))
}
func migrateNodeHeartbeats(tx *bolt.Tx) error {
	if tx.Bucket(NodeHeartbeats) != nil {
		return nil
	}
	if _, err := tx.CreateBucket(NodeHeartbeats); err != nil {
		return err
	}
	return tx.Bucket(Nodes).ForEach(func(id, data []byte) error {
		var node scheduler.Node
		if err := unmarshalNodeWithoutTaskIDs(data, &node); err != nil {
			return err
		}
		return saveHeartbeat(tx, string(id), node.LastPing)
	})
}

func (db *BoltDB) SetNodeState(ctx context.Context, observed *scheduler.Node) (*scheduler.Node, error) {
	var node scheduler.Node
	err := db.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := tx.Bucket(Nodes).Get([]byte(observed.Id))
		if data == nil {
			return scheduler.ErrNodeChanged
		}
		if err := unmarshalNodeWithoutTaskIDs(data, &node); err != nil {
			return err
		}
		if version, err := readNodeVersion(tx, node.Id); err != nil {
			return err
		} else if version != 0 {
			node.Version = version
		}
		if node.Version != observed.Version || loadHeartbeat(tx, node.Id) != observed.LastPing {
			return scheduler.ErrNodeChanged
		}
		if node.State != observed.State {
			node.State = observed.State
			node.Version++
			data, err := proto.Marshal(&node)
			if err != nil {
				return err
			}
			if err := tx.Bucket(Nodes).Put([]byte(node.Id), data); err != nil {
				return err
			}
			usage, err := readNodeUsage(tx, node.Id)
			if err != nil {
				return err
			}
			if err := saveNodeUsageVersion(tx, node.Id, usage, node.Version); err != nil {
				return err
			}
		}
		if err := hydrateNodeProjection(tx, &node, true); err != nil {
			return err
		}
		return nil
	})
	return &node, err
}

func putReportedNode(tx *bolt.Tx, report *scheduler.Node) error {
	if report.Id == "" {
		return fmt.Errorf("node ID is required")
	}
	if err := scheduler.ValidateNodeCapacity(report.Resources); err != nil {
		return err
	}
	var current scheduler.Node
	data := tx.Bucket(Nodes).Get([]byte(report.Id))
	if data == nil {
		if allocations := tx.Bucket(NodeClaims).Bucket([]byte(report.Id)); allocations != nil {
			if key, _ := allocations.Cursor().First(); key != nil {
				return fmt.Errorf("node ID %q still owns active allocations; register with a new ID", report.Id)
			}
		}
	}
	if data != nil {
		if err := unmarshalNodeWithoutTaskIDs(data, &current); err != nil {
			return err
		}
		if version, err := readNodeVersion(tx, current.Id); err != nil {
			return err
		} else if version != 0 {
			current.Version = version
		}
		if current.State == scheduler.NodeState_GONE && report.State != scheduler.NodeState_GONE {
			return fmt.Errorf("gone node %q cannot re-register with the same ID", report.Id)
		}
	}
	if data == nil || scheduler.NodeReportChanged(report, &current) {
		if report.Version != current.Version {
			return scheduler.ErrNodeChanged
		}
		current.Id = report.Id
		scheduler.ApplyNodeReport(&current, report)
		used, err := readNodeUsage(tx, current.Id)
		if err != nil {
			return err
		}
		current.Available = used.Available(current.Resources)
		current.Version++
		current.TaskIds = nil
		stored, err := proto.Marshal(&current)
		if err != nil {
			return err
		}
		if err := tx.Bucket(Nodes).Put([]byte(current.Id), stored); err != nil {
			return err
		}
		if err := saveNodeUsageVersion(tx, current.Id, used, current.Version); err != nil {
			return err
		}
	}
	stamp := time.Now().UnixNano()
	if previous := loadHeartbeat(tx, report.Id); stamp <= previous {
		stamp = previous + 1
	}
	if err := saveHeartbeat(tx, report.Id, stamp); err != nil {
		return err
	}
	// Retain the existing in-process PutNode convention without accepting its assignments.
	if err := hydrateNodeProjection(tx, &current, true); err != nil {
		return err
	}
	report.Version, report.LastPing = current.Version, stamp
	report.TaskIds, report.Available = current.TaskIds, current.Available
	return nil
}
