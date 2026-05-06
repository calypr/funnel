package postgres

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	// List Tasks
	t.Run("ListTasks", func(t *testing.T) {
		req := &tes.ListTasksRequest{
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
}
