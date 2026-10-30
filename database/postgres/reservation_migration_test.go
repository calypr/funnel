package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/proto"
)

func seedLegacyClaimMigration(t *testing.T, db *Postgres, nodes ...*scheduler.Node) {
	t.Helper()
	ctx := context.Background()
	for _, node := range nodes {
		data, err := json.Marshal(node)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.client.Exec(ctx, `UPDATE nodes SET data=$2::jsonb WHERE id=$1`, node.Id, data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.client.Exec(ctx, `DELETE FROM scheduler_migrations WHERE name IN ('authoritative-task-claims-v1','resource-reservations-v1')`); err != nil {
		t.Fatal(err)
	}
}
func assertClaimMigrationPending(t *testing.T, db *Postgres) {
	t.Helper()
	var count int
	if err := db.client.QueryRow(context.Background(), `SELECT count(*) FROM scheduler_migrations WHERE name IN ('authoritative-task-claims-v1','resource-reservations-v1')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration left %d completion markers", count)
	}
}

func TestClaimMigrationConflictDoesNotPartiallyImport(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	good := newTestTask("good", "good", testUser)
	bad := newTestTask("bad", "bad", testUser)
	for _, task := range []*tes.Task{good, bad} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
			t.Fatal(err)
		}
	}
	a := createTryAssignNode(t, db, ctx, "a")
	b := createTryAssignNode(t, db, ctx, "b")
	a.TaskIds = []string{good.Id, bad.Id}
	if _, err := db.client.Exec(ctx, `UPDATE tasks SET claim_owner=$2 WHERE id=$1`, bad.Id, b.Id); err != nil {
		t.Fatal(err)
	}
	seedLegacyClaimMigration(t, db, a, b)
	if err := db.Init(); err == nil {
		t.Fatal("conflicting ownership migrated")
	}
	assertClaimMigrationPending(t, db)
	if owner := taskClaimOwner(t, db, ctx, good.Id); owner != "" {
		t.Fatalf("partial import of %q", owner)
	}
	if owner := taskClaimOwner(t, db, ctx, bad.Id); owner != b.Id {
		t.Fatalf("migration changed conflicting owner: %q", owner)
	}
	var data []byte
	if err := db.client.QueryRow(ctx, `SELECT data FROM nodes WHERE id=$1`, a.Id).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var unchanged scheduler.Node
	if err := json.Unmarshal(data, &unchanged); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a, &unchanged) {
		t.Fatal("failed migration rewrote legacy node")
	}
}

func TestTerminalWriteFailureRollsBackReleaseAndUsesReservationSnapshot(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	task := newTestTask("release-task", "release-task", testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	node := createTryAssignNode(t, db, ctx, "release-node")
	if _, err := db.TryAssign(ctx, task, node); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `CREATE FUNCTION reject_terminal_state() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'forced terminal state failure'; END; $$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `CREATE TRIGGER reject_terminal_state BEFORE UPDATE ON tasks FOR EACH ROW WHEN (NEW.id='release-task' AND NEW.state='COMPLETE') EXECUTE FUNCTION reject_terminal_state()`); err != nil {
		t.Fatal(err)
	}
	err = db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete))
	if err == nil || !strings.Contains(err.Error(), "forced terminal state failure") {
		t.Fatalf("terminal write = %v", err)
	}
	after, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before, after) {
		t.Fatalf("node release survived rollback: before=%v after=%v", before, after)
	}
	var active bool
	var state string
	if err := db.client.QueryRow(ctx, `SELECT state,claim_active FROM tasks WHERE id=$1`, task.Id).Scan(&state, &active); err != nil {
		t.Fatal(err)
	}
	if state != tes.Queued.String() || !active {
		t.Fatalf("task changed on failed terminal write: %s active=%v", state, active)
	}
	if _, err := db.client.Exec(ctx, `DROP TRIGGER reject_terminal_state ON tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `UPDATE tasks SET data=jsonb_set(data,'{resources,cpu_cores}','99'::jsonb) WHERE id=$1`, task.Id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete)); err != nil {
			t.Fatal(err)
		}
	}
	after, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if after.Available.Cpus != 4 || len(after.TaskIds) != 0 {
		t.Fatalf("release ignored stored reservation: %v", after)
	}
	if err := db.client.QueryRow(ctx, `SELECT claim_active FROM tasks WHERE id=$1`, task.Id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("completed allocation remains active")
	}
}

func TestMigrationRetainsMissingOwnerAndDropsTerminalReservations(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	orphan := newTestTask("orphan", "orphan", testUser)
	terminal := newTestTask("terminal", "terminal", testUser)
	for _, task := range []*tes.Task{orphan, terminal} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.WriteEvent(ctx, events.NewState(terminal.Id, tes.Complete)); err != nil {
		t.Fatal(err)
	}
	node := createTryAssignNode(t, db, ctx, "legacy")
	node.TaskIds = []string{terminal.Id}
	if _, err := db.client.Exec(ctx, `UPDATE tasks SET claim_owner='missing' WHERE id=$1`, orphan.Id); err != nil {
		t.Fatal(err)
	}
	seedLegacyClaimMigration(t, db, node)
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if owner, err := db.TryAssign(ctx, orphan, nil); err != nil || owner != "missing" {
		t.Fatalf("missing owner = %q %v", owner, err)
	}
	current, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.TaskIds) != 0 || current.Available.Cpus != 4 {
		t.Fatalf("terminal legacy task retained capacity: %v", current)
	}
	if err := db.WriteEvent(ctx, events.NewState(orphan.Id, tes.SystemError)); err != nil {
		t.Fatal(err)
	}
}

func TestUnauthorizedTerminalWritePreservesAllocation(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	task := newTestTask("private-task", "private-task", testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	node := createTryAssignNode(t, db, ctx, "private-node")
	if _, err := db.TryAssign(ctx, task, node); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	stranger := context.Background()
	auth := server.NewAuthentication(nil, nil, server.AccessOwner)
	_, authErr := auth.Interceptor(stranger, nil, nil, func(authCtx context.Context, _ interface{}) (interface{}, error) { stranger = authCtx; return nil, nil })
	if authErr != nil {
		t.Fatal(authErr)
	}
	if err := db.WriteEvent(stranger, events.NewState(task.Id, tes.Complete)); !errors.Is(err, tes.ErrNotPermitted) {
		t.Fatalf("unauthorized terminal write = %v", err)
	}
	after, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before, after) {
		t.Fatal("unauthorized write released resources")
	}
	var state string
	var active bool
	if err := db.client.QueryRow(ctx, `SELECT state,claim_active FROM tasks WHERE id=$1`, task.Id).Scan(&state, &active); err != nil {
		t.Fatal(err)
	}
	if state != tes.Queued.String() || !active {
		t.Fatalf("unauthorized write changed task: %s active=%v", state, active)
	}
}

func TestClaimLookupAndHeartbeatDoNotWaitForReservationLocks(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	task := newTestTask("locked-task", "locked-task", testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	candidate := createTryAssignNode(t, db, ctx, "locked-node")
	if _, err := db.TryAssign(ctx, task, candidate); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: candidate.Id})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.client.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil {
			t.Errorf("rollback test transaction: %v", err)
		}
	}()
	if _, err := tx.Exec(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, task.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM nodes WHERE id=$1 FOR UPDATE`, candidate.Id); err != nil {
		t.Fatal(err)
	}
	t.Run("claim lookup", func(t *testing.T) {
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if owner, err := db.TryAssign(bounded, task, nil); err != nil || owner != candidate.Id {
			t.Fatalf("read-only lookup blocked behind reservation: %q %v", owner, err)
		}
	})
	t.Run("routine heartbeat", func(t *testing.T) {
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := db.PutNode(bounded, proto.Clone(before).(*scheduler.Node)); err != nil {
			t.Fatalf("heartbeat blocked behind reservation: %v", err)
		}
		after, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: candidate.Id})
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version || after.LastPing <= before.LastPing || !proto.Equal(after.Available, before.Available) {
			t.Fatalf("heartbeat changed reservation or lost liveness: %v", after)
		}
	})
}
