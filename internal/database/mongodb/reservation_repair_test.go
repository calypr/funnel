package mongodb

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/database/mongodb/mongotest"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/identity"
	"github.com/calypr/funnel/internal/logger"
)

func TestEmptyNodeUsageMigrationRepairsIdleResidue(t *testing.T) {
	uri := mongotest.Start(t)
	db := newMongoTestDB(t, uri, "empty_node_usage_repair")
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithUser(context.Background(), &identity.UserInfo{Username: "owner", IsAdmin: true})
	for _, node := range []*scheduler.Node{
		{Id: "idle", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 4, RamGb: 2, DiskGb: 2}},
		{Id: "active", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 2, RamGb: 2, DiskGb: 2}},
	} {
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	activeTask := &tes.Task{Id: "active-task", Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
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
	fullTask := &tes.Task{Id: "full-task", Resources: &tes.Resources{CpuCores: 4, RamGb: 2, DiskGb: 2}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(fullTask)); err != nil {
		t.Fatal(err)
	}
	var activeBefore nodeRecord
	if err := db.nodes().FindOne(ctx, bson.M{"id": "active"}).Decode(&activeBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := db.nodes().UpdateOne(ctx, bson.M{"id": "idle"}, bson.M{"$set": bson.M{
		"reserved": scheduler.ResourceUsage{RAM: 2.220446049250313e-16, Disk: 2.220446049250313e-16},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.collection("scheduler_migrations").DeleteOne(ctx, bson.M{"_id": "empty-node-usage-v1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	var idleAfter, activeAfter nodeRecord
	if err := db.nodes().FindOne(ctx, bson.M{"id": "idle"}).Decode(&idleAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.nodes().FindOne(ctx, bson.M{"id": "active"}).Decode(&activeAfter); err != nil {
		t.Fatal(err)
	}
	if idleAfter.Reserved != (scheduler.ResourceUsage{}) || idleAfter.Node.Version != idle.Version+1 {
		t.Fatalf("idle usage/version after migration = %+v/%d; want zero/%d", idleAfter.Reserved, idleAfter.Node.Version, idle.Version+1)
	}
	if activeAfter.Reserved != activeBefore.Reserved || activeAfter.Node.Version != activeBefore.Node.Version {
		t.Fatalf("active usage/version changed during migration: before=%+v/%d after=%+v/%d", activeBefore.Reserved, activeBefore.Node.Version, activeAfter.Reserved, activeAfter.Node.Version)
	}
	activeNodeAfter, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if activeNodeAfter.Version != activeNode.Version+1 || len(activeNodeAfter.TaskIds) != 1 || activeNodeAfter.TaskIds[0] != activeTask.Id {
		t.Fatalf("active reservation changed during migration: %v", activeNodeAfter)
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
	idleScheduled, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(idleScheduled.TaskIds) != 1 || idleScheduled.TaskIds[0] != fullTask.Id || idleScheduled.Available.Cpus != 0 {
		t.Fatalf("full-capacity scheduler placement after migration = %v", idleScheduled)
	}
	stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: fullTask.Id})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != tes.State_INITIALIZING {
		t.Fatalf("scheduled full-capacity task state = %s; want INITIALIZING", stored.State)
	}
}
