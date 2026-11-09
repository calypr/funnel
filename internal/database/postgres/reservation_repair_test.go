package postgres

import (
	"encoding/json"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
)

func TestEmptyNodeUsageMigrationRepairsIdleResidue(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	for _, node := range []*scheduler.Node{
		{Id: "idle", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 4, RamGb: 2, DiskGb: 2}},
		{Id: "active", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 2, RamGb: 2, DiskGb: 2}},
	} {
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	activeTask := newTestTask("active-task", "active-task", testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(activeTask)); err != nil {
		t.Fatal(err)
	}
	activeNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := db.TryAssign(ctx, activeTask, activeNode); err != nil || owner != activeNode.Id {
		t.Fatalf("reserve active node: owner=%q err=%v", owner, err)
	}
	idle, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	fullTask := newTestTask("full-task", "full-task", testUser)
	fullTask.Resources = &tes.Resources{CpuCores: 4, RamGb: 2, DiskGb: 2}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(fullTask)); err != nil {
		t.Fatal(err)
	}
	var activeUsage scheduler.ResourceUsage
	var activeVersion int64
	var activeUsageJSON []byte
	if err := db.client.QueryRow(ctx, `SELECT version,reserved FROM nodes WHERE id=$1`, "active").Scan(&activeVersion, &activeUsageJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(activeUsageJSON, &activeUsage); err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(scheduler.ResourceUsage{RAM: 2.220446049250313e-16, Disk: 2.220446049250313e-16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `UPDATE nodes SET reserved=$2::jsonb WHERE id=$1`, "idle", string(legacyJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `DELETE FROM scheduler_migrations WHERE name=$1`, "empty-node-usage-v1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	var idleUsage, afterActiveUsage scheduler.ResourceUsage
	var idleVersion, afterActiveVersion int64
	var idleUsageJSON, afterActiveUsageJSON []byte
	if err := db.client.QueryRow(ctx, `SELECT version,reserved FROM nodes WHERE id=$1`, "idle").Scan(&idleVersion, &idleUsageJSON); err != nil {
		t.Fatal(err)
	}
	if err := db.client.QueryRow(ctx, `SELECT version,reserved FROM nodes WHERE id=$1`, "active").Scan(&afterActiveVersion, &afterActiveUsageJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(idleUsageJSON, &idleUsage); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(afterActiveUsageJSON, &afterActiveUsage); err != nil {
		t.Fatal(err)
	}
	if idleUsage != (scheduler.ResourceUsage{}) || idleVersion != idle.Version+1 {
		t.Fatalf("idle usage/version after migration = %+v/%d; want zero/%d", idleUsage, idleVersion, idle.Version+1)
	}
	if afterActiveUsage != activeUsage || afterActiveVersion != activeVersion {
		t.Fatalf("active usage/version changed during migration: before=%+v/%d after=%+v/%d", activeUsage, activeVersion, afterActiveUsage, afterActiveVersion)
	}
	activeAfter, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if activeAfter.Version != activeNode.Version+1 || len(activeAfter.TaskIds) != 1 || activeAfter.TaskIds[0] != activeTask.Id {
		t.Fatalf("active reservation changed during migration: %v", activeAfter)
	}
	s := &scheduler.Scheduler{
		Conf:           appconfig.DefaultConfig().Scheduler,
		Log:            logger.NewLogger("reservation-repair-test", logger.DefaultConfig()),
		Nodes:          db,
		Queue:          db,
		AtomicAssigner: db,
		Event:          db,
	}
	if err := s.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	idleAfter, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(idleAfter.TaskIds) != 1 || idleAfter.TaskIds[0] != fullTask.Id || idleAfter.Available.Cpus != 0 {
		t.Fatalf("full-capacity scheduler placement after migration = %v", idleAfter)
	}
	stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: fullTask.Id})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != tes.State_INITIALIZING {
		t.Fatalf("scheduled full-capacity task state = %s; want INITIALIZING", stored.State)
	}
}
