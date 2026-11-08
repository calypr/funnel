package mongodb

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/funnel/internal/compute/scheduler"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodeSnapshotCompletionRetainsCallerDeadline(t *testing.T) {
	uri := mongotest.Start(t)
	const database = "node_snapshot_deadline"
	db := newMongoTestDB(t, uri, database)
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutNode(t.Context(), &scheduler.Node{Id: "node", State: scheduler.NodeState_ALIVE}); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		command  string
		deadline time.Time
		bounded  bool
	}
	completions := make(chan completion, 4)
	monitored, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(&event.CommandMonitor{
		Started: func(ctx context.Context, evt *event.CommandStartedEvent) {
			if evt.CommandName == "commitTransaction" || evt.CommandName == "abortTransaction" {
				deadline, bounded := ctx.Deadline()
				completions <- completion{evt.CommandName, deadline, bounded}
			}
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	oldClient := db.client
	db.client = monitored
	db.database = monitored.Database(database)
	if err := oldClient.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"get", "list", "missing"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			switch operation {
			case "get":
				_, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "node"})
			case "list":
				_, err = db.ListNodes(ctx, &scheduler.ListNodesRequest{})
			case "missing":
				_, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "missing"})
			}
			if operation == "missing" {
				if status.Code(err) != codes.NotFound {
					t.Fatalf("missing node read = %v, want NotFound", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-completions:
				if !got.bounded || got.deadline.After(deadline) {
					t.Errorf("%s received deadline %s, bounded %t; want caller deadline %s", got.command, got.deadline, got.bounded, deadline)
				}
			case <-ctx.Done():
				t.Fatal("node snapshot did not finish its transaction")
			}
		})
	}
}
