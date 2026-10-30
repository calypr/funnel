package boltdb

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func TestReadQueueProjectsSchedulingFields(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}

	const taskID = "queue-projection"
	inputContent := strings.Repeat("input", 1<<17)
	task := &tes.Task{
		Id:           taskID,
		CreationTime: "2026-01-02T03:04:05Z",
		Description:  "not needed for scheduling",
		Inputs:       []*tes.Input{{Path: "/input", Content: inputContent}},
		Resources:    &tes.Resources{CpuCores: 2, RamGb: 4, DiskGb: 8},
	}
	ctx := context.Background()
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteEvent(ctx, events.NewStdout(taskID, 0, 0, strings.Repeat("x", 1<<20))); err != nil {
		t.Fatal(err)
	}
	var taskData []byte
	if err := db.db.View(func(tx *bolt.Tx) error {
		taskData = append([]byte(nil), tx.Bucket(TaskBucket).Get([]byte(taskID))...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	projectedData, err := taskSchedulingProjection(taskData)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectedData) >= len(taskData) || bytes.Contains(projectedData, []byte(inputContent)) {
		t.Fatalf("queue wire projection retained the %d-byte input from a %d-byte task", len(inputContent), len(taskData))
	}
	projectedTask := &tes.Task{}
	if err := proto.Unmarshal(projectedData, projectedTask); err != nil {
		t.Fatal(err)
	}
	if projectedTask.Id != taskID || projectedTask.CreationTime != task.CreationTime || projectedTask.Resources == nil || projectedTask.Resources.CpuCores != 2 {
		t.Fatalf("projected task = %+v", projectedTask)
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
	fullTask, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullTask.Inputs) != 1 || fullTask.Inputs[0].Content != inputContent {
		t.Fatal("full task view no longer retains input content")
	}
}
