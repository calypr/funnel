package schedulercontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/database/mongodb"
	"github.com/calypr/funnel/database/postgres"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// store is the backend-neutral surface used by scheduler contract cases.
type store interface {
	scheduler.SchedulerServiceServer
	scheduler.TaskQueue
	scheduler.AtomicTaskAssigner
	scheduler.NodeHealthStore
	events.Writer
	GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error)
}

type storeFactory func(*testing.T) store

func TestSchedulerContract(t *testing.T) {
	selected := os.Getenv("FUNNEL_SCHEDULER_TEST_BACKEND")
	if selected != "" && selected != "boltdb" && selected != "postgres" && selected != "mongodb" {
		t.Fatalf("invalid FUNNEL_SCHEDULER_TEST_BACKEND %q; want empty, boltdb, postgres, or mongodb", selected)
	}

	if selected == "" || selected == "boltdb" {
		t.Run("boltdb", func(t *testing.T) {
			runContract(t, newBoltStore)
		})
	}
	if selected == "" || selected == "postgres" {
		t.Run("postgres", func(t *testing.T) {
			runPostgresContract(t)
		})
	}
	if selected == "" || selected == "mongodb" {
		t.Run("mongodb", func(t *testing.T) {
			uri := mongotest.Start(t)
			var nextDatabase int
			runContract(t, func(t *testing.T) store {
				nextDatabase++
				conf := config.DefaultConfig().MongoDB
				conf.Addrs = []string{uri}
				conf.Database = fmt.Sprintf("scheduler_contract_%d", nextDatabase)
				db, err := mongodb.NewMongoDB(conf)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(db.Close)
				if err := db.Init(); err != nil {
					t.Fatal(err)
				}
				return db
			})
		})
	}

}

func runContract(t *testing.T, open storeFactory) {
	runReservationContracts(t, open)
	runExecutionClaimContracts(t, open)
	t.Run("node pages use stable IDs and traverse the full fleet", func(t *testing.T) {
		db := open(t)
		testNodePagination(t, db)
	})

	t.Run("assignment does not refresh node liveness", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "heartbeat-task")
		node := createNode(t, db, ctx, "heartbeat-node", 2)
		before, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
		if err != nil {
			t.Fatal(err)
		}

		owner, err := db.TryAssign(ctx, task, before)
		if err != nil || owner != node.Id {
			t.Fatalf("TryAssign = (%q, %v); want (%q, nil)", owner, err, node.Id)
		}
		after, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
		if err != nil {
			t.Fatal(err)
		}
		if after.LastPing != before.LastPing {
			t.Fatalf("assignment LastPing = %d; want unchanged %d", after.LastPing, before.LastPing)
		}
	})

	t.Run("assignments progress through repeated heartbeat writes", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		taskIDs := []string{"heartbeat-progress-a", "heartbeat-progress-b", "heartbeat-progress-c"}
		for _, id := range taskIDs {
			createTask(t, db, ctx, id)
		}
		if _, err := db.PutNode(ctx, &scheduler.Node{
			Id:        "heartbeat-progress-node",
			State:     scheduler.NodeState_ALIVE,
			Resources: &scheduler.Resources{Cpus: 3, RamGb: 3, DiskGb: 3},
		}); err != nil {
			t.Fatalf("create heartbeat-progress-node: %v", err)
		}

		heartbeats := &heartbeatBeforeAssignment{AtomicTaskAssigner: db, nodes: db}
		s := newScheduler(db, db, heartbeats, db)
		if err := s.Schedule(ctx); err != nil {
			t.Fatalf("Schedule: %v", err)
		}
		if heartbeats.writes != len(taskIDs) {
			t.Fatalf("heartbeat writes = %d; want one before each of %d assignments", heartbeats.writes, len(taskIDs))
		}

		assigned := assignedTasks(t, ctx, db)
		for _, taskID := range taskIDs {
			owners := assigned[taskID]
			if len(owners) != 1 || owners[0] != "heartbeat-progress-node" {
				t.Fatalf("task %s owners = %v; want exactly one owner on heartbeat-progress-node", taskID, owners)
			}
			if state := getTask(t, db, ctx, taskID).State; state != tes.State_INITIALIZING {
				t.Fatalf("task %s state = %s; want INITIALIZING", taskID, state)
			}
		}
		if len(assigned) != len(taskIDs) {
			t.Fatalf("assigned tasks = %v; want exactly %d tasks", assigned, len(taskIDs))
		}
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "heartbeat-progress-node"})
		if err != nil {
			t.Fatal(err)
		}
		if len(node.TaskIds) != len(taskIDs) || node.Available.GetCpus() != 0 {
			t.Fatalf("node task IDs/resources = %v/%v; want %d tasks and 0 CPUs available", node.TaskIds, node.Available, len(taskIDs))
		}
	})

	t.Run("concurrent schedulers assign one task once", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createTask(t, db, ctx, "concurrent-task")
		createNode(t, db, ctx, "node-a", 2)
		createNode(t, db, ctx, "node-b", 2)

		queueBarrier := newRendezvous(t, "queue read")
		nodeBarrier := newRendezvous(t, "assignment node read")
		var done sync.WaitGroup
		done.Add(2)
		for i := 0; i < 2; i++ {
			nodes := &nodeReadBarrier{
				SchedulerServiceServer: db,
				barrier:                nodeBarrier,
				reverse:                i == 1,
			}
			s := newScheduler(nodes, &queueReadBarrier{TaskQueue: db, barrier: queueBarrier}, db, db)
			go func() {
				defer done.Done()
				if err := s.Schedule(ctx); err != nil {
					t.Errorf("Schedule: %v", err)
				}
			}()
		}
		done.Wait()

		assigned := assignedTasks(t, ctx, db)
		owners := assigned["concurrent-task"]
		if len(owners) != 1 {
			t.Fatalf("task assignments = %v; want exactly one owner", assigned)
		}
	})

	t.Run("recovers a committed assignment before initializing", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		const taskID = "claimed-task"
		const nodeID = "claimed-node"
		task := createTask(t, db, ctx, taskID)
		createNode(t, db, ctx, nodeID, 2)
		candidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
		if err != nil {
			t.Fatal(err)
		}
		if owner, err := db.TryAssign(ctx, task, candidate); err != nil || owner != nodeID {
			t.Fatalf("record transactional assignment = (%q, %v); want (%q, nil)", owner, err, nodeID)
		}

		// TryAssign committed the claim and reservation; the process stopped before
		// it could write the INITIALIZING event.
		if err := newScheduler(db, db, db, db).Schedule(ctx); err != nil {
			t.Fatalf("recover claimed task: %v", err)
		}
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
		if err != nil {
			t.Fatal(err)
		}
		if len(node.TaskIds) != 1 || node.TaskIds[0] != taskID {
			t.Fatalf("recovered node task IDs = %v; want exactly [%s]", node.TaskIds, taskID)
		}
		if owners := assignedTasks(t, ctx, db)[taskID]; len(owners) != 1 || owners[0] != nodeID {
			t.Fatalf("recovered task owners = %v; want exactly [%s]", owners, nodeID)
		}
		storedTask := getTask(t, db, ctx, taskID)
		if storedTask.State != tes.State_INITIALIZING {
			t.Fatalf("recovered task state = %s; want %s", storedTask.State, tes.State_INITIALIZING)
		}
	})

	t.Run("failed initializing event recovers without duplicate assignment", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		const taskID = "state-recovery-task"
		createTask(t, db, ctx, taskID)
		createNode(t, db, ctx, "state-node-a", 1)
		createNode(t, db, ctx, "state-node-b", 1)

		writer := &failInitializingOnce{Writer: db}
		s := newScheduler(db, db, db, writer)
		if err := s.Schedule(ctx); err != nil {
			t.Fatalf("first Schedule: %v", err)
		}
		if !writer.failed {
			t.Fatal("INITIALIZING event did not fail as injected")
		}
		if got := getTask(t, db, ctx, taskID).State; got != tes.State_QUEUED {
			t.Fatalf("task state after failed event = %s; want QUEUED", got)
		}

		if err := s.Schedule(ctx); err != nil {
			t.Fatalf("recovery Schedule: %v", err)
		}
		assigned := assignedTasks(t, ctx, db)
		owners := assigned[taskID]
		if len(owners) != 1 {
			t.Fatalf("task assignments after recovery = %v; want exactly one owner", assigned)
		}
		assignedNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: owners[0]})
		if err != nil {
			t.Fatal(err)
		}
		if len(assignedNode.TaskIds) != 1 || assignedNode.Available.GetCpus() != 0 {
			t.Fatalf("assigned node tasks/resources = %v/%v; want one task and 0 CPUs available", assignedNode.TaskIds, assignedNode.Available)
		}
		if got := getTask(t, db, ctx, taskID).State; got != tes.State_INITIALIZING {
			t.Fatalf("task state after recovery = %s; want INITIALIZING", got)
		}
	})

	t.Run("lost assignment response recovers without duplicate reservation", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		const taskID = "lost-response-task"
		createTask(t, db, ctx, taskID)
		createNode(t, db, ctx, "lost-response-node", 4)

		assigner := &loseAssignmentResponseOnce{AtomicTaskAssigner: db}
		s := newScheduler(db, db, assigner, db)
		if err := s.Schedule(ctx); err != nil {
			t.Fatalf("first Schedule: %v", err)
		}
		if !assigner.lost {
			t.Fatal("TryAssign response was not lost as injected")
		}
		if got := getTask(t, db, ctx, taskID).State; got != tes.State_QUEUED {
			t.Fatalf("task state after lost response = %s; want QUEUED", got)
		}

		if err := s.Schedule(ctx); err != nil {
			t.Fatalf("recovery Schedule: %v", err)
		}
		assigned := assignedTasks(t, ctx, db)
		owners := assigned[taskID]
		if len(owners) != 1 || owners[0] != "lost-response-node" {
			t.Fatalf("task assignments after recovery = %v; want one owner on lost-response-node", assigned)
		}
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "lost-response-node"})
		if err != nil {
			t.Fatal(err)
		}
		if len(node.TaskIds) != 1 || node.Available.GetCpus() != 3 {
			t.Fatalf("recovered node tasks/resources = %v/%v; want one task and 3 CPUs available", node.TaskIds, node.Available)
		}
		if got := getTask(t, db, ctx, taskID).State; got != tes.State_INITIALIZING {
			t.Fatalf("task state after recovery = %s; want INITIALIZING", got)
		}
	})

	t.Run("concurrent schedulers do not over-reserve node resources", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		taskIDs := []string{"cpu-task-a", "cpu-task-b"}
		for _, id := range taskIDs {
			createTask(t, db, ctx, id)
		}
		createNode(t, db, ctx, "single-cpu-node", 1)

		queueBarrier := newRendezvous(t, "distinct queued tasks")
		nodeBarrier := newRendezvous(t, "shared node snapshot")
		writes := &assignmentWriteCounter{}
		var done sync.WaitGroup
		done.Add(len(taskIDs))
		for _, taskID := range taskIDs {
			taskID := taskID
			nodes := &nodeReadBarrier{SchedulerServiceServer: db, barrier: nodeBarrier}
			queue := &queueReadBarrier{TaskQueue: db, barrier: queueBarrier, taskID: taskID}
			assigner := &countingAssigner{AtomicTaskAssigner: db, counter: writes}
			s := newScheduler(nodes, queue, assigner, db)
			go func() {
				defer done.Done()
				if err := s.Schedule(ctx); err != nil {
					t.Errorf("Schedule for %s: %v", taskID, err)
				}
			}()
		}
		done.Wait()

		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "single-cpu-node"})
		if err != nil {
			t.Fatal(err)
		}
		if len(node.TaskIds) != 1 || (node.TaskIds[0] != taskIDs[0] && node.TaskIds[0] != taskIDs[1]) {
			t.Fatalf("node task IDs = %v; want exactly one of %v", node.TaskIds, taskIDs)
		}
		if node.Available.GetCpus() != 0 {
			t.Fatalf("node has %v CPUs available after assignment; want 0", node.Available.GetCpus())
		}
		assignedID := node.TaskIds[0]
		initializing := 0
		for _, taskID := range taskIDs {
			state := getTask(t, db, ctx, taskID).State
			if state == tes.State_INITIALIZING {
				initializing++
				if taskID != assignedID {
					t.Fatalf("unassigned task %s reached INITIALIZING", taskID)
				}
			} else if state != tes.State_QUEUED {
				t.Fatalf("unassigned task %s state = %s; want QUEUED", taskID, state)
			}
		}
		if initializing != 1 {
			t.Fatalf("%d tasks reached INITIALIZING; want exactly one", initializing)
		}
		attempts, success, failure := writes.snapshot()
		if attempts != 2 || success != 1 || failure != 1 {
			t.Fatalf("transactional assignments = %d attempts, %d successes, %d conflicts; want 2, 1, 1", attempts, success, failure)
		}
	})

	t.Run("concurrent assignments retry on alternate node", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		taskIDs := []string{"alternate-task-a", "alternate-task-b"}
		for _, id := range taskIDs {
			createTask(t, db, ctx, id)
		}
		createNode(t, db, ctx, "node-a", 1)
		createNode(t, db, ctx, "node-b", 1)

		queueBarrier := newRendezvous(t, "distinct queued tasks")
		nodeBarrier := newRendezvous(t, "shared node snapshot")
		writes := &assignmentWriteCounter{}
		var done sync.WaitGroup
		done.Add(len(taskIDs))
		for _, taskID := range taskIDs {
			taskID := taskID
			nodes := &nodeReadBarrier{
				SchedulerServiceServer: db,
				barrier:                nodeBarrier,
				preferredNode:          "node-a",
			}
			queue := &queueReadBarrier{TaskQueue: db, barrier: queueBarrier, taskID: taskID}
			assigner := &countingAssigner{AtomicTaskAssigner: db, counter: writes}
			s := newScheduler(nodes, queue, assigner, db)
			go func() {
				defer done.Done()
				if err := s.Schedule(ctx); err != nil {
					t.Errorf("Schedule for %s: %v", taskID, err)
				}
			}()
		}
		done.Wait()

		assigned := assignedTasks(t, ctx, db)
		assignedCount := 0
		var pendingID string
		for _, id := range taskIDs {
			owners := assigned[id]
			if len(owners) == 0 {
				pendingID = id
				continue
			}
			if len(owners) != 1 || owners[0] != "node-a" {
				t.Fatalf("first-pass owners for %s = %v; want one owner on node-a", id, owners)
			}
			assignedCount++
		}
		if assignedCount != 1 || pendingID == "" {
			t.Fatalf("first-pass assignments = %v; want exactly one task on node-a", assigned)
		}
		if state := getTask(t, db, ctx, pendingID).State; state != tes.State_QUEUED {
			t.Fatalf("pending task %s state = %s; want QUEUED", pendingID, state)
		}
		if attempts, success, failure := writes.snapshot(); attempts != 2 || success != 1 || failure != 1 {
			t.Fatalf("first-pass assignments = %d attempts, %d successes, %d conflicts; want 2, 1, 1", attempts, success, failure)
		}

		if err := newScheduler(db, db, db, db).Schedule(ctx); err != nil {
			t.Fatalf("retry pending task: %v", err)
		}
		assigned = assignedTasks(t, ctx, db)
		ownersA, ownersB := assigned[taskIDs[0]], assigned[taskIDs[1]]
		pendingOwners := assigned[pendingID]
		if len(ownersA) != 1 || len(ownersB) != 1 || ownersA[0] == ownersB[0] || len(pendingOwners) != 1 || pendingOwners[0] != "node-b" {
			t.Fatalf("assignments after retry = %v; want one task on each node and %s on node-b", assigned, pendingID)
		}
		for _, taskID := range taskIDs {
			if got := getTask(t, db, ctx, taskID).State; got != tes.State_INITIALIZING {
				t.Fatalf("task %s state = %s; want INITIALIZING", taskID, got)
			}
		}
	})
}

func runPostgresContract(t *testing.T) {
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelStartup()

	container, err := postgrescontainer.Run(startupCtx,
		"postgres:16-alpine",
		postgrescontainer.WithDatabase("postgres"),
		postgrescontainer.WithUsername("postgres"),
		postgrescontainer.WithPassword("example"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate PostgreSQL container: %v", err)
		}
	})
	containerHost, err := container.Host(startupCtx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(startupCtx, "5432")
	if err != nil {
		t.Fatal(err)
	}
	host := fmt.Sprintf("%s:%s", containerHost, port.Port())
	var nextDatabase int
	open := func(t *testing.T) store {
		nextDatabase++
		database := fmt.Sprintf("scheduler_contract_%d", nextDatabase)
		databaseCtx, cancelDatabase := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelDatabase()
		admin, err := pgx.Connect(databaseCtx, postgresURL(host, "postgres", "example", "postgres"))
		if err != nil {
			t.Fatalf("connect to PostgreSQL admin database: %v", err)
		}
		if _, err := admin.Exec(databaseCtx, "CREATE DATABASE "+database); err != nil {
			admin.Close(databaseCtx)
			t.Fatalf("create PostgreSQL test database: %v", err)
		}
		if err := admin.Close(databaseCtx); err != nil {
			t.Fatalf("close PostgreSQL admin connection: %v", err)
		}
		conf := &config.Postgres{
			Host:     host,
			Database: database,
			User:     "postgres",
			Password: "example",
			Timeout: &config.TimeoutConfig{
				TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(5 * time.Second)},
			},
		}
		db, err := postgres.NewPostgres(conf)
		if err != nil {
			t.Fatalf("create Postgres store: %v", err)
		}
		if err := db.Init(); err != nil {
			db.Close()
			t.Fatalf("initialize Postgres store: %v", err)
		}
		t.Cleanup(db.Close)
		return db
	}
	runContract(t, open)
}

func newBoltStore(t *testing.T) store {
	db, err := boltdb.NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatalf("create BoltDB store: %v", err)
	}
	if err := db.Init(); err != nil {
		db.Close()
		t.Fatalf("initialize BoltDB store: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func postgresURL(host, user, password, database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s", user, password, host, database)
}

func testContext() context.Context {
	return context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{
		Username: "scheduler-contract",
		IsAdmin:  true,
	})
}

func testTask(id string) *tes.Task {
	return &tes.Task{
		Id:   id,
		Name: id,
		Executors: []*tes.Executor{{
			Image:   "alpine",
			Command: []string{"echo", "hello"},
		}},
		Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1},
	}
}

func createTask(t *testing.T, db store, ctx context.Context, id string) *tes.Task {
	t.Helper()
	task := testTask(id)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatalf("create task %s: %v", id, err)
	}
	return task
}

func createNode(t *testing.T, db store, ctx context.Context, id string, cpus uint32) *scheduler.Node {
	t.Helper()
	node := &scheduler.Node{
		Id:        id,
		State:     scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{Cpus: cpus, RamGb: 2, DiskGb: 2},
	}
	if _, err := db.PutNode(ctx, node); err != nil {
		t.Fatalf("create node %s: %v", id, err)
	}
	return node
}

func newScheduler(nodes scheduler.SchedulerServiceServer, queue scheduler.TaskQueue, assigner scheduler.AtomicTaskAssigner, writer events.Writer) *scheduler.Scheduler {
	return &scheduler.Scheduler{
		Conf:           config.DefaultConfig().Scheduler,
		Log:            logger.NewLogger("scheduler-contract", logger.DefaultConfig()),
		Nodes:          nodes,
		Queue:          queue,
		AtomicAssigner: assigner,
		Event:          writer,
	}
}

func getTask(t *testing.T, db store, ctx context.Context, id string) *tes.Task {
	t.Helper()
	task, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id})
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	return task
}

func assignedTasks(t *testing.T, ctx context.Context, db store) map[string][]string {
	t.Helper()
	nodes, err := scheduler.ListAllNodes(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	assigned := make(map[string][]string)
	for _, node := range nodes {
		for _, id := range node.TaskIds {
			assigned[id] = append(assigned[id], node.Id)
		}
	}
	return assigned
}

func testNodePagination(t *testing.T, db store) {
	t.Helper()
	ctx := testContext()
	for _, id := range []string{"cursor-a", "cursor-b", "cursor-c", "cursor-d"} {
		createNode(t, db, ctx, id, 1)
	}

	first, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertNodeIDs(t, first.Nodes, "cursor-a", "cursor-b")
	if first.NextPageToken == "" {
		t.Fatal("first page has no continuation token")
	}
	assertHTTPNodePagination(t, db, first.NextPageToken)

	second, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	assertNodeIDs(t, second.Nodes, "cursor-c", "cursor-d")
	if second.NextPageToken != "" {
		t.Fatalf("exact-multiple final page has token %q", second.NextPageToken)
	}
	if _, err := db.DeleteNode(ctx, &scheduler.Node{Id: "cursor-b"}); err != nil {
		t.Fatalf("delete cursor node: %v", err)
	}
	deletedCursor, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	assertNodeIDs(t, deletedCursor.Nodes, "cursor-c", "cursor-d")
	if deletedCursor.NextPageToken != "" {
		t.Fatalf("page after deleted cursor has token %q", deletedCursor.NextPageToken)
	}

	createNode(t, db, ctx, "cursor-e", 1)
	partial, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	assertNodeIDs(t, partial.Nodes, "cursor-a", "cursor-c", "cursor-d")
	if partial.NextPageToken == "" {
		t.Fatal("partial page did not have a continuation token")
	}
	partialFinal, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 3, PageToken: partial.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	assertNodeIDs(t, partialFinal.Nodes, "cursor-e")
	if partialFinal.NextPageToken != "" {
		t.Fatalf("partial final page has token %q", partialFinal.NextPageToken)
	}

	if _, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative page_size error = %v, want InvalidArgument", err)
	}
	if _, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: scheduler.MaxNodePageSize + 1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized page_size error = %v, want InvalidArgument", err)
	}
	if _, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageToken: "invalid"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid page_token error = %v, want InvalidArgument", err)
	}

	for i := 0; i < 1005; i++ {
		createNode(t, db, ctx, fmt.Sprintf("many-%04d", i), 1)
	}
	defaultPage, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultPage.Nodes) != scheduler.DefaultNodePageSize || defaultPage.NextPageToken == "" {
		t.Fatalf("default page = %d nodes, token %q", len(defaultPage.Nodes), defaultPage.NextPageToken)
	}

	all, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: scheduler.MaxNodePageSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Nodes) != scheduler.MaxNodePageSize || all.NextPageToken == "" {
		t.Fatalf("first maximum page = %d nodes, token %q", len(all.Nodes), all.NextPageToken)
	}
	remaining, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{
		PageSize:  scheduler.MaxNodePageSize,
		PageToken: all.NextPageToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Nodes) != 9 || remaining.NextPageToken != "" {
		t.Fatalf("last page = %d nodes, token %q; want 9 nodes and no token", len(remaining.Nodes), remaining.NextPageToken)
	}

	nodes, err := scheduler.ListAllNodes(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1009 {
		t.Fatalf("ListAllNodes returned %d nodes, want 1009", len(nodes))
	}
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if seen[node.Id] {
			t.Fatalf("ListAllNodes repeated node %q", node.Id)
		}
		seen[node.Id] = true
	}
	if len(seen) != 1009 {
		t.Fatalf("ListAllNodes contained %d unique IDs, want 1009", len(seen))
	}
}

func assertHTTPNodePagination(t *testing.T, db store, expectedToken string) {
	t.Helper()
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, server.NewMarshaler()))
	if err := scheduler.RegisterSchedulerServiceHandlerServer(context.Background(), mux, db); err != nil {
		t.Fatalf("register scheduler HTTP gateway: %v", err)
	}

	first := serveNodeListRequest(t, mux, "page_size=2")
	assertHTTPNodeIDs(t, first, "cursor-a", "cursor-b")
	if token := responseToken(t, first); token != expectedToken {
		t.Fatalf("HTTP next_page_token = %q, want %q", token, expectedToken)
	}

	second := serveNodeListRequest(t, mux, "page_size=2&page_token="+url.QueryEscape(expectedToken))
	assertHTTPNodeIDs(t, second, "cursor-c", "cursor-d")
	if token := responseToken(t, second); token != "" {
		t.Fatalf("HTTP final next_page_token = %q, want empty", token)
	}

	for _, query := range []string{"page_size=1001", "page_token=invalid"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/nodes?"+query, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET /v1/nodes?%s status = %d, want 400: %s", query, response.Code, response.Body.String())
		}
	}
}

func serveNodeListRequest(t *testing.T, mux *runtime.ServeMux, query string) map[string]json.RawMessage {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/nodes?"+query, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /v1/nodes?%s status = %d: %s", query, response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode node list response: %v", err)
	}
	return body
}

func assertHTTPNodeIDs(t *testing.T, body map[string]json.RawMessage, want ...string) {
	t.Helper()
	var nodes []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body["nodes"], &nodes); err != nil {
		t.Fatalf("decode response nodes: %v", err)
	}
	if len(nodes) != len(want) {
		t.Fatalf("HTTP page has %d nodes, want %d", len(nodes), len(want))
	}
	for i, id := range want {
		if nodes[i].ID != id {
			t.Fatalf("HTTP node %d ID = %q, want %q", i, nodes[i].ID, id)
		}
	}
}

func responseToken(t *testing.T, body map[string]json.RawMessage) string {
	t.Helper()
	encoded, ok := body["next_page_token"]
	if !ok {
		t.Fatalf("response has no next_page_token field: %v", body)
	}
	var token string
	if err := json.Unmarshal(encoded, &token); err != nil {
		t.Fatalf("decode next_page_token: %v", err)
	}
	return token
}

func assertNodeIDs(t *testing.T, nodes []*scheduler.Node, want ...string) {
	t.Helper()
	if len(nodes) != len(want) {
		t.Fatalf("page has %d nodes, want %d: %v", len(nodes), len(want), nodes)
	}
	for i, id := range want {
		if nodes[i].Id != id {
			t.Fatalf("node %d has ID %q, want %q", i, nodes[i].Id, id)
		}
	}
}

type rendezvous struct {
	mu      sync.Mutex
	arrived int
	ready   chan struct{}
	name    string
	t       *testing.T
}

func newRendezvous(t *testing.T, name string) *rendezvous {
	return &rendezvous{ready: make(chan struct{}), name: name, t: t}
}

func (b *rendezvous) wait() {
	b.mu.Lock()
	b.arrived++
	if b.arrived == 2 {
		close(b.ready)
	}
	b.mu.Unlock()

	select {
	case <-b.ready:
	case <-time.After(10 * time.Second):
		b.t.Errorf("timed out waiting for both schedulers at %s", b.name)
	}
}

type queueReadBarrier struct {
	scheduler.TaskQueue
	barrier *rendezvous
	taskID  string
}

func (q *queueReadBarrier) ReadQueue(n int, after *tes.Task) []*tes.Task {
	tasks := q.TaskQueue.ReadQueue(n, after)
	if q.taskID != "" {
		selected := make([]*tes.Task, 0, 1)
		for _, task := range tasks {
			if task.Id == q.taskID {
				selected = append(selected, task)
			}
		}
		tasks = selected
	}
	q.barrier.wait()
	return tasks
}

type nodeReadBarrier struct {
	scheduler.SchedulerServiceServer
	barrier       *rendezvous
	preferredNode string
	reverse       bool
	calls         int
}

func (n *nodeReadBarrier) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	resp, err := n.SchedulerServiceServer.ListNodes(ctx, req)
	if err != nil {
		return nil, err
	}
	n.calls++
	// Each scheduler now reads one snapshot before it reads the queue.
	if n.calls == 1 {
		if n.reverse {
			for left, right := 0, len(resp.Nodes)-1; left < right; left, right = left+1, right-1 {
				resp.Nodes[left], resp.Nodes[right] = resp.Nodes[right], resp.Nodes[left]
			}
		}
		if n.preferredNode != "" {
			for i, node := range resp.Nodes {
				if node.Id == n.preferredNode {
					resp.Nodes[0], resp.Nodes[i] = resp.Nodes[i], resp.Nodes[0]
					break
				}
			}
		}
		n.barrier.wait()
	}
	return resp, nil
}

func (n *nodeReadBarrier) SetNodeState(ctx context.Context, observed *scheduler.Node) (*scheduler.Node, error) {
	return n.SchedulerServiceServer.(scheduler.NodeHealthStore).SetNodeState(ctx, observed)
}

// heartbeatBeforeAssignment performs a real GetNode/PutNode heartbeat after the
// scheduler has read its candidate snapshot and before its atomic assignment.
type heartbeatBeforeAssignment struct {
	scheduler.AtomicTaskAssigner
	nodes  scheduler.SchedulerServiceServer
	writes int
}

func (a *heartbeatBeforeAssignment) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	if candidate != nil {
		node, err := a.nodes.GetNode(ctx, &scheduler.GetNodeRequest{Id: candidate.Id})
		if err != nil {
			return "", fmt.Errorf("get node for heartbeat: %w", err)
		}
		version, stamp := node.Version, node.LastPing
		_, err = a.nodes.PutNode(ctx, node)
		if err != nil {
			return "", fmt.Errorf("write competing heartbeat: %w", err)
		}
		if node.Version != version || node.LastPing <= stamp {
			return "", fmt.Errorf("heartbeat changed reservation version or failed to advance liveness")
		}
		a.writes++
	}
	return a.AtomicTaskAssigner.TryAssign(ctx, task, candidate)
}

type assignmentWriteCounter struct {
	mu       sync.Mutex
	attempts int
	success  int
	failure  int
}

func (c *assignmentWriteCounter) record(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if err == nil {
		c.success++
	} else {
		c.failure++
	}
}

func (c *assignmentWriteCounter) snapshot() (attempts, success, failure int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts, c.success, c.failure
}

type countingAssigner struct {
	scheduler.AtomicTaskAssigner
	counter *assignmentWriteCounter
}

func (a *countingAssigner) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	owner, err := a.AtomicTaskAssigner.TryAssign(ctx, task, candidate)
	if candidate != nil {
		a.counter.record(err)
	}
	return owner, err
}

type failInitializingOnce struct {
	events.Writer
	failed bool
}

func (w *failInitializingOnce) WriteEvent(ctx context.Context, event *events.Event) error {
	if event.Type == events.Type_TASK_STATE && event.GetState() == tes.State_INITIALIZING && !w.failed {
		w.failed = true
		return errors.New("injected INITIALIZING event failure")
	}
	return w.Writer.WriteEvent(ctx, event)
}

type loseAssignmentResponseOnce struct {
	scheduler.AtomicTaskAssigner
	lost bool
}

func (a *loseAssignmentResponseOnce) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	owner, err := a.AtomicTaskAssigner.TryAssign(ctx, task, candidate)
	if err == nil && owner != "" && !a.lost {
		a.lost = true
		return "", errors.New("injected lost response after committed assignment")
	}
	return owner, err
}
