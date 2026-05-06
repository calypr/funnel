package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
)

func TestQuotes(t *testing.T) {
	c := tests.DefaultConfig()
	c.Compute = "local"
	f := tests.NewFunnel(c)
	f.StartServer()

	fixturesDir, err := filepath.Abs(filepath.Join("..", "fixtures", "quotes"))
	if err != nil {
		t.Fatalf("Failed to resolve absolute path for fixtures directory: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(fixturesDir, "*.json"))
	if err != nil {
		t.Fatalf("Failed to read fixtures directory: %v", err)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			task := loadTaskFromFile(t, file)
			t.Logf("Submitting task %s", file)

			// Submit task
			id, err := f.RunTask(task)
			if err != nil {
				t.Fatalf("Failed to run task %s: %v", file, err)
			}

			// Create TES client
			client, err := tes.NewClient(c.Server.HTTPAddress())
			if err != nil {
				t.Fatalf("Failed to create TES client: %v", err)
			}

			// Wait for task to complete
			ctx := context.Background()
			err = client.WaitForTask(ctx, id)
			if err != nil {
				t.Fatalf("Task %s did not complete successfully: %v", filepath.Base(file), err)
			}

			// Fetch task
			task, err = client.GetTask(ctx, &tes.GetTaskRequest{
				Id:   id,
				View: tes.View_FULL.String(),
			})
			if err != nil {
				t.Fatalf("Failed to fetch task %s: %v", filepath.Base(file), err)
			}

			// Check task state
			if task.State != tes.State_COMPLETE {
				t.Fatalf("Task %s did not complete successfully. State: %s", filepath.Base(file), task.State)
			}

			// Check task logs
			if len(task.Logs) == 0 || len(task.Logs[0].Logs) == 0 {
				t.Fatalf("Task %s has no logs", filepath.Base(file))
			}
		})
	}
}

// Loads task from a JSON file
func loadTaskFromFile(t *testing.T, file string) *tes.Task {
	t.Helper()

	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("Failed to open fixture file %s: %v", file, err)
	}
	defer f.Close()

	var task tes.Task
	if err := json.NewDecoder(f).Decode(&task); err != nil {
		t.Fatalf("Failed to decode fixture file %s: %v", file, err)
	}

	return &task
}
