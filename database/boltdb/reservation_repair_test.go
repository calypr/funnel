package boltdb

import (
	"context"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

func TestEmptyNodeUsageMigrationRepairsIdleResidue(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := context.Background()
	for _, node := range []*scheduler.Node{
		{Id: "idle", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 4, RamGb: 2, DiskGb: 2}},
		{Id: "active", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 2, RamGb: 2, DiskGb: 2}},
	} {
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	activeTask := createTryAssignTask(t, db, "active-task")
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
	fullTask := &tes.Task{Id: "full-task", Resources: &tes.Resources{CpuCores: 4, RamGb: 2, DiskGb: 2}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(fullTask)); err != nil {
		t.Fatal(err)
	}
	var activeUsage scheduler.ResourceUsage
	var activeVersion int64
	if err := db.db.View(func(tx *bolt.Tx) error {
		var err error
		activeUsage, err = readNodeUsage(tx, "active")
		if err != nil {
			return err
		}
		activeVersion, err = readNodeVersion(tx, "active")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		if err := saveNodeUsageVersion(tx, "idle", scheduler.ResourceUsage{RAM: 2.220446049250313e-16, Disk: 2.220446049250313e-16}, idle.Version); err != nil {
			return err
		}
		return tx.Bucket(resourceUsageMigrations).Delete([]byte(emptyNodeUsageMigration))
	}); err != nil {
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
	if err := db.db.View(func(tx *bolt.Tx) error {
		var err error
		idleUsage, err = readNodeUsage(tx, "idle")
		if err != nil {
			return err
		}
		idleVersion, err = readNodeVersion(tx, "idle")
		if err != nil {
			return err
		}
		afterActiveUsage, err = readNodeUsage(tx, "active")
		if err != nil {
			return err
		}
		afterActiveVersion, err = readNodeVersion(tx, "active")
		return err
	}); err != nil {
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
		Conf:           config.DefaultConfig().Scheduler,
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
