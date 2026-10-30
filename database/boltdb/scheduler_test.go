package boltdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func TestReadQueueContinuesAfterRemovedTask(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: id})); err != nil {
			t.Fatal(err)
		}
	}
	first := db.ReadQueue(2, nil)
	if len(first) != 2 || first[1].Id != "b" {
		t.Fatalf("first page = %v", first)
	}
	if err := db.WriteEvent(ctx, events.NewState("b", tes.State_INITIALIZING)); err != nil {
		t.Fatal(err)
	}
	next := db.ReadQueue(2, first[1])
	if len(next) != 1 || next[0].Id != "c" {
		t.Fatalf("next page = %v, want c", next)
	}
}
