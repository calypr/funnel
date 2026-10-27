package mongodb

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoReservationTransactions(t *testing.T) {
	uri := mongotest.Start(t)
	sequence := 0
	open := func(t *testing.T) *MongoDB {
		t.Helper()
		sequence++
		db := newMongoTestDB(t, uri, fmt.Sprintf("reservations_%d", sequence))
		if err := db.Init(); err != nil {
			t.Fatal(err)
		}
		return db
	}
	ctx := context.WithValue(context.Background(), server.UserInfoKey, server.UserInfo{Username: "owner", IsAdmin: true})
	seed := func(t *testing.T, db *MongoDB) (*tes.Task, *scheduler.Node) {
		t.Helper()
		task := &tes.Task{Id: "task", State: tes.Queued, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
		if err := db.insertTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		node := &scheduler.Node{Id: "node", State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 2, RamGb: 2, DiskGb: 2}}
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
		return task, node
	}
	readTask := func(t *testing.T, db *MongoDB) *taskRecord {
		t.Helper()
		var task taskRecord
		if err := db.tasks().FindOne(ctx, bson.M{"id": "task"}).Decode(&task); err != nil {
			t.Fatal(err)
		}
		return &task
	}
	readNode := func(t *testing.T, db *MongoDB) *scheduler.Node {
		t.Helper()
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "node"})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	validator := func(t *testing.T, db *MongoDB, collection string, rule bson.M) {
		t.Helper()
		if err := db.database.RunCommand(ctx, bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: rule}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("failed node write rolls back the new claim", func(t *testing.T) {
		db := open(t)
		task, node := seed(t, db)
		validator(t, db, "nodes", bson.M{"taskids": bson.M{"$size": 0}})
		if _, err := db.TryAssign(ctx, task, node); err == nil {
			t.Fatal("assignment bypassed node validator")
		}
		if stored := readTask(t, db); stored.ClaimOwner != "" || stored.ClaimActive {
			t.Fatalf("partial claim survived: %+v", stored)
		}
		if got := readNode(t, db); got.Available.Cpus != 2 || len(got.TaskIds) != 0 {
			t.Fatalf("failed assignment changed node: %v", got)
		}
		validator(t, db, "nodes", bson.M{})
		if owner, err := db.TryAssign(ctx, task, node); err != nil || owner != node.Id {
			t.Fatalf("retry = %q, %v", owner, err)
		}
	})
	t.Run("failed terminal state write rolls back resource release", func(t *testing.T) {
		db := open(t)
		task, node := seed(t, db)
		if _, err := db.TryAssign(ctx, task, node); err != nil {
			t.Fatal(err)
		}
		before := readNode(t, db)
		validator(t, db, "tasks", bson.M{"state": bson.M{"$ne": tes.Complete}})
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete)); err == nil {
			t.Fatal("completion bypassed task validator")
		}
		stored := readTask(t, db)
		if stored.Task.State != tes.Queued || !stored.ClaimActive {
			t.Fatalf("failed completion changed claim/state: %+v", stored)
		}
		got := readNode(t, db)
		if got.Version != before.Version || got.Available.Cpus != 1 || len(got.TaskIds) != 1 {
			t.Fatalf("failed completion released capacity: %v", got)
		}
		validator(t, db, "tasks", bson.M{})
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete)); err != nil {
			t.Fatal(err)
		}
		if got := readNode(t, db); got.Available.Cpus != 2 || len(got.TaskIds) != 0 {
			t.Fatalf("retry did not release: %v", got)
		}
	})
	t.Run("release uses original reservation and cannot be replayed", func(t *testing.T) {
		db := open(t)
		task, node := seed(t, db)
		if _, err := db.TryAssign(ctx, task, node); err != nil {
			t.Fatal(err)
		}
		if _, err := db.tasks().UpdateOne(ctx, bson.M{"id": task.Id}, bson.M{"$set": bson.M{"resources": &tes.Resources{CpuCores: 99, RamGb: 99, DiskGb: 99}}}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Complete)); err != nil {
				t.Fatal(err)
			}
		}
		if got := readNode(t, db); got.Available.Cpus != 2 || got.Available.RamGb != 2 || len(got.TaskIds) != 0 {
			t.Fatalf("release used edited task or repeated release: %v", got)
		}
		if err := db.insertTask(ctx, task); err == nil {
			t.Fatal("claimed task ID was recreated")
		}
		stored := readTask(t, db)
		if stored.ClaimOwner != node.Id || stored.ClaimActive || stored.Task.State != tes.Complete {
			t.Fatalf("ownership/state lost: %+v", stored)
		}
	})
	t.Run("unauthorized completion preserves reservation", func(t *testing.T) {
		db := open(t)
		task, node := seed(t, db)
		if _, err := db.TryAssign(ctx, task, node); err != nil {
			t.Fatal(err)
		}
		stranger := context.Background()
		auth := server.NewAuthentication(nil, nil, server.AccessOwner)
		_, authErr := auth.Interceptor(stranger, nil, nil, func(authCtx context.Context, _ interface{}) (interface{}, error) { stranger = authCtx; return nil, nil })
		if authErr != nil {
			t.Fatal(authErr)
		}
		if err := db.WriteEvent(stranger, events.NewState(task.Id, tes.Complete)); err != tes.ErrNotPermitted {
			t.Fatalf("unauthorized completion = %v", err)
		}
		if got := readNode(t, db); got.Available.Cpus != 1 || len(got.TaskIds) != 1 {
			t.Fatalf("unauthorized release: %v", got)
		}
	})
	t.Run("conflicting legacy owners abort entire migration", func(t *testing.T) {
		db := open(t)
		task, node := seed(t, db)
		if _, err := db.collection("scheduler_migrations").DeleteMany(ctx, bson.M{}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.nodes().UpdateOne(ctx, bson.M{"id": node.Id}, bson.M{"$set": bson.M{"taskids": []string{task.Id}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.nodes().InsertOne(ctx, &scheduler.Node{Id: "other", TaskIds: []string{task.Id}, Resources: node.Resources}); err != nil {
			t.Fatal(err)
		}
		if err := db.Init(); err == nil {
			t.Fatal("ambiguous legacy owners accepted")
		}
		if stored := readTask(t, db); stored.ClaimOwner != "" || stored.ClaimActive {
			t.Fatalf("migration partially committed: %+v", stored)
		}
		if count, err := db.collection("scheduler_migrations").CountDocuments(ctx, bson.M{"done": true}); err != nil || count != 0 {
			t.Fatalf("migration marker = %d, %v", count, err)
		}
		if _, err := db.nodes().DeleteOne(ctx, bson.M{"id": "other"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Init(); err != nil {
			t.Fatal(err)
		}
		if owner, err := db.TryAssign(ctx, task, nil); err != nil || owner != node.Id {
			t.Fatalf("imported claim = %q, %v", owner, err)
		}
		before := readNode(t, db)
		if err := db.Init(); err != nil {
			t.Fatal(err)
		}
		if after := readNode(t, db); after.Version != before.Version || after.Available.Cpus != 1 || len(after.TaskIds) != 1 {
			t.Fatalf("repeated migration changed allocation: %v", after)
		}
	})
}

func TestMongoRequiresTransactions(t *testing.T) {
	db := newMongoTestDB(t, mongotest.StartStandalone(t), "standalone")
	if err := db.Init(); err == nil || !strings.Contains(err.Error(), "replica set") {
		t.Fatalf("standalone startup error = %v; want replica-set requirement", err)
	}
}
