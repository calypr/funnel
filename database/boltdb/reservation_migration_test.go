package boltdb

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// Seed the old storage format, not a heartbeat on the upgraded API.
func seedLegacyClaimMigration(t *testing.T, db *BoltDB, nodes ...*scheduler.Node) {
	t.Helper()
	if err := db.db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{NodeClaims, NodeUsage} {
			if tx.Bucket(name) != nil {
				if err := tx.DeleteBucket(name); err != nil {
					return err
				}
			}
		}
		for _, node := range nodes {
			data, err := proto.Marshal(node)
			if err != nil {
				return err
			}
			if err := tx.Bucket(Nodes).Put([]byte(node.Id), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClaimMigrationRejectsConflictingOwnersAtomically(t *testing.T) {
	for _, kind := range []string{"multiple nodes", "conflicting durable claim"} {
		t.Run(kind, func(t *testing.T) {
			db := openTryAssignDB(t)
			good := createTryAssignTask(t, db, "good")
			bad := createTryAssignTask(t, db, "bad")
			a := createTryAssignNode(t, db, "a")
			b := createTryAssignNode(t, db, "b")
			a.TaskIds = []string{good.Id, bad.Id}
			if kind == "multiple nodes" {
				b.TaskIds = []string{bad.Id}
			} else {
				if err := db.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(TaskClaims).Put([]byte(bad.Id), []byte(b.Id)) }); err != nil {
					t.Fatal(err)
				}
			}
			seedLegacyClaimMigration(t, db, a, b)
			if err := db.Init(); err == nil {
				t.Fatal("conflicting migration succeeded")
			}
			if err := db.db.View(func(tx *bolt.Tx) error {
				if tx.Bucket(NodeClaims) != nil || tx.Bucket(NodeUsage) != nil {
					t.Fatal("failed migration left a completion marker")
				}
				if tx.Bucket(TaskClaims).Get([]byte(good.Id)) != nil {
					t.Fatal("failed migration partially imported claims")
				}
				data := tx.Bucket(Nodes).Get([]byte(a.Id))
				var node scheduler.Node
				if err := proto.Unmarshal(data, &node); err != nil {
					return err
				}
				if !proto.Equal(a, &node) {
					t.Fatal("failed migration rewrote legacy node")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReleaseReservationRollsBackWithTaskStateAndUsesSnapshot(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := context.Background()
	task := createTryAssignTask(t, db, "task")
	node := createTryAssignNode(t, db, "node")
	if _, err := db.TryAssign(ctx, task, node); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	forced := errors.New("abort after release and state mutation")
	err = db.db.Update(func(tx *bolt.Tx) error {
		if err := transitionTaskState(tx, task.Id, tes.Complete); err != nil {
			return err
		}
		return forced
	})
	if !errors.Is(err, forced) {
		t.Fatalf("rollback = %v", err)
	}
	after, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before, after) {
		t.Fatalf("aborted transaction changed node: before=%v after=%v", before, after)
	}
	if err := db.db.View(func(tx *bolt.Tx) error {
		if getTaskState(tx, task.Id) != tes.Queued {
			t.Fatal("aborted transaction changed state")
		}
		if tx.Bucket(NodeClaims).Bucket([]byte(node.Id)).Get([]byte(task.Id)) == nil {
			t.Fatal("aborted transaction removed allocation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		var stored tes.Task
		if err := proto.Unmarshal(tx.Bucket(TaskBucket).Get([]byte(task.Id)), &stored); err != nil {
			return err
		}
		stored.Resources.CpuCores = 99
		data, err := proto.Marshal(&stored)
		if err != nil {
			return err
		}
		return tx.Bucket(TaskBucket).Put([]byte(task.Id), data)
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete)); err != nil {
			t.Fatal(err)
		}
	}
	after, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if after.Available.Cpus != 4 || len(after.TaskIds) != 0 {
		t.Fatalf("release ignored reservation snapshot: %v", after)
	}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err == nil {
		t.Fatal("recreated an allocated task")
	}
}

func TestMigrationKeepsMissingOwnerAndExcludesTerminalAssignments(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := context.Background()
	orphan := createTryAssignTask(t, db, "missing-owner-task")
	terminal := createTryAssignTask(t, db, "terminal-task")
	if err := db.WriteEvent(ctx, events.NewState(terminal.Id, tes.Complete)); err != nil {
		t.Fatal(err)
	}
	node := createTryAssignNode(t, db, "legacy")
	node.TaskIds = []string{terminal.Id}
	if err := db.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(TaskClaims).Put([]byte(orphan.Id), []byte("missing")) }); err != nil {
		t.Fatal(err)
	}
	seedLegacyClaimMigration(t, db, node)
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if owner, err := db.TryAssign(ctx, orphan, nil); err != nil || owner != "missing" {
		t.Fatalf("orphan owner = %q %v", owner, err)
	}
	current, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.TaskIds) != 0 || current.Available.Cpus != 4 {
		t.Fatalf("terminal legacy task retained capacity: %v", current)
	}
	if err := db.WriteEvent(ctx, events.NewState(orphan.Id, tes.SystemError)); err != nil {
		t.Fatal(err)
	}
}
