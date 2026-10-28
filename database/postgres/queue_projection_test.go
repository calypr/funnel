package postgres

import (
	"strings"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
)

func TestReadQueueProjectsSchedulingFields(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate PostgreSQL container: %v", err)
		}
	})

	const taskID = "queue-projection"
	task := &tes.Task{
		Id:          taskID,
		Description: "not needed for scheduling",
		Inputs:      []*tes.Input{{Path: "/input", Content: strings.Repeat("large-input", 1<<16)}},
		Logs:        []*tes.TaskLog{{Logs: []*tes.ExecutorLog{{Stdout: strings.Repeat("x", 1<<20)}}}},
		Resources:   &tes.Resources{CpuCores: 2, RamGb: 4, DiskGb: 8},
	}
	if err := db.WriteEvent(getUserContext(), events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}

	got := db.ReadQueue(1, nil)
	if len(got) != 1 {
		t.Fatalf("ReadQueue returned %d tasks, want 1", len(got))
	}
	queued := got[0]
	if queued.Id != taskID || queued.CreationTime == "" || queued.State != tes.State_QUEUED {
		t.Fatalf("queue fields = id %q, creation time %q, state %s", queued.Id, queued.CreationTime, queued.State)
	}
	if queued.Resources == nil || queued.Resources.CpuCores != 2 || queued.Resources.RamGb != 4 || queued.Resources.DiskGb != 8 {
		t.Fatalf("queue resources = %v", queued.Resources)
	}
	if queued.Description != "" || queued.Inputs != nil || queued.Logs != nil {
		t.Fatalf("ReadQueue returned non-scheduling task fields: %+v", queued)
	}

	var indexDefinition string
	if err := db.client.QueryRow(getUserContext(), `SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_tasks_queue_order'`).Scan(&indexDefinition); err != nil {
		t.Fatalf("read queue index definition: %v", err)
	}
	if !strings.Contains(indexDefinition, "creation_time") || !strings.Contains(indexDefinition, "id") || !strings.Contains(indexDefinition, "WHERE") {
		t.Fatalf("queue index is not the partial ordering index: %s", indexDefinition)
	}
}
