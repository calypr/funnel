package boltdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func TestPutNodeVersionOptimisticConcurrency(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "nodes.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	initial := &scheduler.Node{Id: "node-1", State: scheduler.NodeState_ALIVE}
	if _, err := db.PutNode(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if initial.Version != 1 {
		t.Fatalf("initial version = %d, want 1", initial.Version)
	}

	stale := proto.Clone(initial).(*scheduler.Node)
	fresh := proto.Clone(initial).(*scheduler.Node)
	fresh.Metadata = map[string]string{"writer": "fresh"}
	if _, err := db.PutNode(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Version != 2 {
		t.Fatalf("updated version = %d, want 2", fresh.Version)
	}

	stale.Metadata = map[string]string{"writer": "stale"}
	if _, err := db.PutNode(ctx, stale); err == nil {
		t.Fatal("stale node update succeeded, want version conflict")
	}

	stored, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: initial.Id})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != 2 || stored.Metadata["writer"] != "fresh" {
		t.Fatalf("stored node = version %d, metadata %v; want version 2 and fresh writer", stored.Version, stored.Metadata)
	}
}

func TestPutNodeUpgradesStoredVersionZeroNode(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "nodes.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}

	legacy := &scheduler.Node{Id: "legacy-node", State: scheduler.NodeState_ALIVE}
	encoded, err := proto.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(Nodes).Put([]byte(legacy.Id), encoded)
	}); err != nil {
		t.Fatal(err)
	}

	update := proto.Clone(legacy).(*scheduler.Node)
	update.Metadata = map[string]string{"initialized": "true"}
	if _, err := db.PutNode(context.Background(), update); err != nil {
		t.Fatalf("updating stored version-zero node: %v", err)
	}
	if update.Version != 1 {
		t.Fatalf("upgraded version = %d, want 1", update.Version)
	}
}
