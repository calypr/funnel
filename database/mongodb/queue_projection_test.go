package mongodb

import (
	"strings"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
)

func TestReadQueueProjectsSchedulingFields(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "queue_projection")
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}

	const taskID = "queue-projection"
	task := &tes.Task{
		Id:           taskID,
		CreationTime: "2026-01-02T03:04:05Z",
		State:        tes.State_QUEUED,
		Description:  "not needed for scheduling",
		Inputs:       []*tes.Input{{Path: "/input", Content: strings.Repeat("large-input", 1<<16)}},
		Logs:         []*tes.TaskLog{{Logs: []*tes.ExecutorLog{{Stdout: strings.Repeat("x", 1<<20)}}}},
		Resources:    &tes.Resources{CpuCores: 2, RamGb: 4, DiskGb: 8},
	}
	if err := db.insertTask(executionClaimContext("owner"), task); err != nil {
		t.Fatal(err)
	}

	got := db.ReadQueue(1, nil)
	if len(got) != 1 {
		t.Fatalf("ReadQueue returned %d tasks, want 1", len(got))
	}
	queued := got[0]
	if queued.Id != taskID || queued.CreationTime != task.CreationTime || queued.State != tes.State_QUEUED {
		t.Fatalf("queue fields = id %q, creation time %q, state %s", queued.Id, queued.CreationTime, queued.State)
	}
	if queued.Resources == nil || queued.Resources.CpuCores != 2 || queued.Resources.RamGb != 4 || queued.Resources.DiskGb != 8 {
		t.Fatalf("queue resources = %v", queued.Resources)
	}
	if queued.Description != "" || queued.Inputs != nil || queued.Logs != nil {
		t.Fatalf("ReadQueue returned non-scheduling task fields: %+v", queued)
	}
}

func TestReadQueueContinuesAfterRemovedTask(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "queue_cursor")
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := executionClaimContext("owner")
	for _, id := range []string{"queue-a", "queue-b", "queue-c"} {
		task := &tes.Task{Id: id, CreationTime: "2026-01-02T03:04:05Z", State: tes.State_QUEUED}
		if err := db.insertTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	first := db.ReadQueue(2, nil)
	if len(first) != 2 || first[0].Id != "queue-a" || first[1].Id != "queue-b" {
		t.Fatalf("first page = %v, want queue-a then queue-b", first)
	}
	if err := db.WriteEvent(ctx, events.NewState("queue-b", tes.State_INITIALIZING)); err != nil {
		t.Fatal(err)
	}
	next := db.ReadQueue(2, first[1])
	if len(next) != 1 || next[0].Id != "queue-c" {
		t.Fatalf("next page = %v, want queue-c", next)
	}
}
