package postgres

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/types/known/durationpb"
)

const testUser = "example"
const testTaskID = "example"

// Helper function to create a context with a mock user.
func getUserContext() context.Context {
	user := server.UserInfo{
		Username: testUser,
		IsAdmin:  false,
		IsPublic: false,
	}

	ctx := context.WithValue(context.Background(), server.UserInfoKey, user)

	return ctx
}

// newTestTask creates a valid tes.Task object for testing.
func newTestTask(id, name, owner string) *tes.Task {
	return &tes.Task{
		Id:   id,
		Name: name,
		// TODO: Handle owner/auth here...
		// Owner: owner,
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello"},
			},
		},
		Resources: &tes.Resources{
			CpuCores: 1,
			DiskGb:   1.0,
			RamGb:    1.0,
		},
	}
}

func getTestPostgres(t *testing.T) (*Postgres, testcontainers.Container) {
	conf := &config.Postgres{
		Host:     "localhost:5432",
		Database: "funnel",
		User:     "funnel",
		Password: "example",

		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{
				Duration: durationpb.New(5 * time.Second),
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(conf.Database),
		postgres.WithUsername(conf.User),
		postgres.WithPassword(conf.Password),
		postgres.BasicWaitStrategies(),
	)

	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}

	host, err := postgresContainer.Host(ctx)
	if err != nil {
		testcontainers.TerminateContainer(postgresContainer)
		t.Fatalf("failed to get container host: %v", err)
	}

	port, err := postgresContainer.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("Failed to get mapped port: %v", err)
	}

	conf.Host = fmt.Sprintf("%s:%s", host, port.Port())

	db, err := NewPostgres(conf)
	if err != nil {
		// Clean up the container
		testcontainers.TerminateContainer(postgresContainer)
		t.Fatalf("Failed to create Postgres instance: %v", err)
	}

	if err := db.Init(); err != nil {
		db.Close()
		testcontainers.TerminateContainer(postgresContainer)
		t.Fatalf("Failed to initialize database schema: %v", err)
	}

	return db, postgresContainer
}

// TestPostgresOperations tests the core CRUD functionality for tasks.
func TestPostgresOperations(t *testing.T) {
	db, container := getTestPostgres(t)

	t.Cleanup(func() {
		db.Close()
		log.Printf("Terminating Postgres Container...")
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Fatalf("Failed to terminate container: %v", err)
		}
	})

	task := newTestTask(testTaskID, "Example Task", testUser)
	ctx := getUserContext()

	// Create Task
	t.Run("CreateTask", func(t *testing.T) {
		event := events.NewTaskCreated(task)
		if err := db.WriteEvent(ctx, event); err != nil {
			t.Fatalf("Failed to create task: %v", err)
		}
	})

	// Get Task
	t.Run("GetTask", func(t *testing.T) {
		req := &tes.GetTaskRequest{Id: testTaskID, View: tes.View_FULL.String()}
		fetchedTask, err := db.GetTask(ctx, req)

		if err != nil {
			t.Fatalf("Failed to get task: %v", err)
		}
		if fetchedTask.Id != testTaskID {
			t.Errorf("Mismatched Task ID. Expected %s, Got %s", testTaskID, fetchedTask.Id)
		}
		if fetchedTask.State != tes.State_QUEUED {
			t.Errorf("Mismatched Task State. Expected %s, Got %s", tes.State_QUEUED, fetchedTask.State)
		}
	})

	// Cancel Task
	t.Run("CancelTask", func(t *testing.T) {
		event := events.NewState(testTaskID, tes.State_CANCELED)
		if err := db.WriteEvent(ctx, event); err != nil {
			t.Fatalf("Failed to cancel task: %v", err)
		}
	})

	// Check Canceled Task
	t.Run("CheckCancel", func(t *testing.T) {
		req := &tes.GetTaskRequest{Id: testTaskID, View: tes.View_MINIMAL.String()}
		fetchedTask, err := db.GetTask(ctx, req)

		if err != nil {
			t.Fatalf("Failed to get canceled task: %v", err)
		}
		if fetchedTask.State != tes.State_CANCELED {
			t.Errorf("Task state not CANCELED. Expected %s, Got %s", tes.State_CANCELED, fetchedTask.State)
		}
	})

	// List Tasks
	t.Run("ListTasks", func(t *testing.T) {
		req := &tes.ListTasksRequest{
			State:    tes.State_CANCELED,
			PageSize: 10,
			View:     tes.View_MINIMAL.String(),
		}
		resp, err := db.ListTasks(ctx, req)
		if err != nil {
			t.Fatalf("Failed to list tasks: %v", err)
		}
		if len(resp.Tasks) != 1 {
			t.Fatalf("Expected 1 task in list, got %d", len(resp.Tasks))
		}
		if resp.Tasks[0].Id != testTaskID {
			t.Errorf("ListTasks returned wrong ID: %s", resp.Tasks[0].Id)
		}

		req = &tes.ListTasksRequest{
			State:    tes.State_COMPLETE,
			PageSize: 10,
			View:     tes.View_MINIMAL.String(),
		}
		resp, err = db.ListTasks(ctx, req)
		if err != nil {
			t.Fatalf("Failed to list tasks: %v", err)
		}
		if len(resp.Tasks) != 0 { // We only created one task and it's canceled, so there should be 0 complete tasks.
			t.Fatalf("Expected 0 tasks in list, got %d", len(resp.Tasks))
		}
	})
}

func TestReadQueueContinuesAfterRemovedTask(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() { db.Close(); _ = testcontainers.TerminateContainer(container) })
	ctx := getUserContext()
	for _, id := range []string{"queue-a", "queue-b", "queue-c"} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(id, id, testUser))); err != nil {
			t.Fatal(err)
		}
	}
	// Tasks with the same timestamp must still be visited in ID order.
	_, err := db.client.Exec(ctx, `UPDATE tasks SET creation_time = '2026-01-01T00:00:00Z', data = jsonb_set(data, '{creation_time}', to_jsonb('2026-01-01T00:00:00Z'::text)) WHERE id LIKE 'queue-%'`)
	if err != nil {
		t.Fatal(err)
	}
	first := db.ReadQueue(2, nil)
	if len(first) != 2 {
		t.Fatalf("first page: got %d tasks, want 2", len(first))
	}
	if err := db.WriteEvent(ctx, events.NewState(first[1].Id, tes.State_INITIALIZING)); err != nil {
		t.Fatal(err)
	}
	next := db.ReadQueue(2, first[1])
	if len(next) != 1 || next[0].Id != "queue-c" {
		t.Fatalf("next page: got %v, want queue-c", next)
	}
}

func TestPostgresTaskViews(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminating Postgres container: %v", err)
		}
	})

	ctx := getUserContext()
	task := &tes.Task{
		Id:          "view-task",
		State:       tes.Queued,
		Name:        "view test",
		Description: "task description",
		Inputs:      []*tes.Input{{Name: "input", Content: "secret input"}},
		Outputs:     []*tes.Output{{Name: "output", Url: "file:///tmp/output", Path: "/output"}},
		Executors:   []*tes.Executor{{Image: "alpine", Command: []string{"echo", "hello"}}},
		Logs: []*tes.TaskLog{{
			Logs:       []*tes.ExecutorLog{{StartTime: "2026-10-26T00:00:00Z", Stdout: "secret stdout", Stderr: "secret stderr"}},
			SystemLogs: []string{"secret system log"},
		}},
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.client.Exec(ctx, `INSERT INTO tasks (id, state, owner, data) VALUES ($1, $2, $3, $4::jsonb)`, task.Id, task.State.String(), testUser, data)
	if err != nil {
		t.Fatal(err)
	}

	for _, view := range []string{"", tes.View_MINIMAL.String(), tes.View_BASIC.String(), tes.View_FULL.String()} {
		t.Run(map[string]string{"": "default-minimal", tes.View_MINIMAL.String(): "minimal", tes.View_BASIC.String(): "basic", tes.View_FULL.String(): "full"}[view], func(t *testing.T) {
			got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: view})
			if err != nil {
				t.Fatal(err)
			}
			assertPostgresTaskView(t, got, view)

			listed, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: view, PageSize: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Tasks) != 1 {
				t.Fatalf("listed %d tasks, want one", len(listed.Tasks))
			}
			assertPostgresTaskView(t, listed.Tasks[0], view)
		})
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "GetTask", call: func() error { _, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: "HUGE"}); return err }},
		{name: "ListTasks", call: func() error { _, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "HUGE"}); return err }},
	} {
		t.Run("invalid-"+tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || !strings.Contains(err.Error(), "unknown task view") {
				t.Fatalf("invalid view error = %v", err)
			}
		})
	}
}

func assertPostgresTaskView(t *testing.T, task *tes.Task, view string) {
	t.Helper()
	if task.Id != "view-task" || task.State != tes.Queued {
		t.Fatalf("view omitted ID/state: %+v", task)
	}
	switch view {
	case "", tes.View_MINIMAL.String():
		if task.Name != "" || task.Description != "" || task.Inputs != nil || task.Outputs != nil || task.Executors != nil || task.Resources != nil || task.Logs != nil {
			t.Fatalf("minimal view returned non-ID/state fields: %+v", task)
		}
	case tes.View_BASIC.String():
		if task.Name != "view test" || len(task.Inputs) != 1 || task.Inputs[0].Content != "" || len(task.Outputs) != 1 || len(task.Executors) != 1 || task.Logs[0].Logs[0].Stdout != "" || task.Logs[0].Logs[0].Stderr != "" || task.Logs[0].SystemLogs != nil {
			t.Fatalf("basic view included forbidden fields or omitted basic fields: %+v", task)
		}
	case tes.View_FULL.String():
		if task.Name != "view test" || task.Inputs[0].Content != "secret input" || task.Logs[0].Logs[0].Stdout != "secret stdout" || task.Logs[0].Logs[0].Stderr != "secret stderr" || len(task.Logs[0].SystemLogs) != 1 {
			t.Fatalf("full view omitted full task data: %+v", task)
		}
	}
}
