package postgres

import (
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
)

func TestPostgresRejectsStaleHeartbeatAndCorruptNodeRow(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate PostgreSQL container: %v", err)
		}
	})
	ctx := getUserContext()
	task := newTestTask("postgres-recovery", "postgres-recovery", testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	staleNodes := make(map[string]*scheduler.Node)
	for _, id := range []string{"node-a", "node-b"} {
		if _, err := db.PutNode(ctx, &scheduler.Node{
			Id:        id,
			State:     scheduler.NodeState_ALIVE,
			Resources: &scheduler.Resources{Cpus: 1, RamGb: 4, DiskGb: 4},
		}); err != nil {
			t.Fatalf("create node %s: %v", id, err)
		}
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		staleNodes[id] = node
	}

	s := &scheduler.Scheduler{
		Conf:           config.DefaultConfig().Scheduler,
		Log:            logger.NewLogger("test", logger.DefaultConfig()),
		Nodes:          db,
		Queue:          db,
		AtomicAssigner: db,
		Event:          db,
	}
	if err := s.Schedule(ctx); err != nil {
		t.Fatalf("schedule task: %v", err)
	}

	resp, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	assigned := 0
	assignedNode := ""
	for _, node := range resp.Nodes {
		for _, id := range node.TaskIds {
			if id == task.Id {
				assigned++
				assignedNode = node.Id
			}
		}
	}
	if assigned != 1 {
		t.Fatalf("task assigned to %d nodes; want exactly one", assigned)
	}
	if _, err := db.PutNode(ctx, staleNodes[assignedNode]); err == nil {
		t.Fatal("stale heartbeat overwrote the assignment")
	}
	storedTask, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id})
	if err != nil || storedTask.State != tes.State_INITIALIZING {
		t.Fatalf("task state = (%v, %v); want INITIALIZING", storedTask, err)
	}
	if _, err := db.client.Exec(ctx, `UPDATE nodes SET data = '"invalid"'::jsonb WHERE id = $1`, assignedNode); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{}); err == nil {
		t.Fatal("expected an error rather than a partial node list")
	}
}
