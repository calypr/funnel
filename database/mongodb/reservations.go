package mongodb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

var (
	_ scheduler.AtomicTaskAssigner = (*MongoDB)(nil)
	_ scheduler.NodeHealthStore    = (*MongoDB)(nil)
)

// Inline preserves the existing BSON task/node schema. Claim fields are internal
// and never become part of the TES response.
type taskRecord struct {
	Task             *tes.Task                `bson:",inline"`
	Owner            string                   `bson:"owner"`
	ClaimOwner       string                   `bson:"claim_owner,omitempty"`
	ClaimActive      bool                     `bson:"claim_active"`
	ClaimResources   *scheduler.ResourceUsage `bson:"claim_resources,omitempty"`
	ExecutionClaimed bool                     `bson:"execution_claimed"`
}

type nodeRecord struct {
	Node     scheduler.Node          `bson:",inline"`
	Reserved scheduler.ResourceUsage `bson:"reserved"`
}

func (db *MongoDB) transaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	ctx, cancel := db.wrap(ctx)
	defer cancel()
	session, err := db.client.StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(context.Background())
	return session.WithTransaction(ctx, fn, options.Transaction().
		SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
}

func (db *MongoDB) saveReservedNode(ctx context.Context, record *nodeRecord) error {
	version := record.Node.Version + 1
	available := record.Reserved.Available(record.Node.Resources)
	filter := bson.M{"id": record.Node.Id, "version": record.Node.Version}
	if record.Node.Version == 0 {
		filter = bson.M{"id": record.Node.Id, "$or": bson.A{
			bson.M{"version": 0},
			bson.M{"version": bson.M{"$exists": false}},
		}}
	}
	result, err := db.nodes().UpdateOne(ctx,
		filter,
		bson.M{
			"$set":   bson.M{"reserved": record.Reserved, "available": available, "version": version},
			"$unset": bson.M{"taskids": "", "task_ids": "", "taskIds": ""},
		},
		options.UpdateOne().SetCollation(&options.Collation{Locale: "simple"}),
	)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("reservation node %q changed or disappeared", record.Node.Id)
	}
	record.Node.Version = version
	record.Node.Available = available
	record.Node.TaskIds = nil
	return nil
}

func (db *MongoDB) replaceReportedNode(ctx context.Context, record *nodeRecord) error {
	result, err := db.nodes().ReplaceOne(ctx, bson.M{"id": record.Node.Id}, record, options.Replace().SetCollation(&options.Collation{Locale: "simple"}))
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("node %q disappeared", record.Node.Id)
	}
	return nil
}

// TryAssign commits ownership and capacity together. MongoDB retries the whole
// transaction on write conflicts, rechecking the task and node on each attempt.
func (db *MongoDB) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	if task == nil || task.Id == "" {
		return "", fmt.Errorf("try assign: task ID is required")
	}
	assign := func(ctx context.Context) (any, error) {
		var stored taskRecord
		findOptions := options.FindOne().SetProjection(bson.M{"id": 1, "state": 1, "resources": 1, "claim_owner": 1, "claim_active": 1})
		if candidate == nil {
			findOptions.SetProjection(bson.M{"state": 1, "claim_owner": 1, "claim_active": 1})
		}
		err := db.tasks().FindOne(ctx, bson.M{"id": task.Id}, findOptions).Decode(&stored)
		if err == mongo.ErrNoDocuments {
			return "", nil
		}
		if err != nil {
			return nil, err
		}
		if stored.Task.State != tes.State_QUEUED {
			return "", nil
		}
		if stored.ClaimOwner != "" {
			if !stored.ClaimActive {
				return nil, fmt.Errorf("task %q has an inactive allocation", task.Id)
			}
			return stored.ClaimOwner, nil
		}
		if candidate == nil || candidate.Id == "" {
			return "", nil
		}
		var node nodeRecord
		nodeProjection := bson.M{"id": 1, "state": 1, "resources": 1, "preemptible": 1, "zone": 1, "metadata": 1, "version": 1, "reserved": 1}
		if err := db.nodes().FindOne(ctx, bson.M{"id": candidate.Id}, options.FindOne().SetProjection(nodeProjection).SetCollation(&options.Collation{Locale: "simple"})).Decode(&node); err != nil {
			return nil, err
		}
		node.Node.Available = node.Reserved.Available(node.Node.Resources)
		if !scheduler.Match(&node.Node, stored.Task, scheduler.DefaultPredicates) {
			return nil, fmt.Errorf("candidate node %q is no longer eligible", candidate.Id)
		}
		held, err := scheduler.TaskResourceUsage(stored.Task)
		if err != nil {
			return nil, err
		}
		node.Reserved, err = node.Reserved.Add(held)
		if err != nil {
			return nil, err
		}
		node.Node.TaskIds = nil
		if _, err := db.tasks().UpdateOne(ctx, bson.M{"id": task.Id}, bson.M{"$set": bson.M{
			"claim_owner": candidate.Id, "claim_active": true, "claim_resources": held,
		}}); err != nil {
			return nil, err
		}
		if err := db.saveReservedNode(ctx, &node); err != nil {
			return nil, err
		}
		return candidate.Id, nil
	}
	var result any
	var err error
	if candidate == nil {
		// Recovery lookups do not lock or write either reservation document.
		ctx, cancel := db.wrap(ctx)
		defer cancel()
		result, err = assign(ctx)
	} else {
		result, err = db.transaction(ctx, assign)
	}
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (db *MongoDB) transitionReservedTask(ctx context.Context, id string, target tes.State) error {
	_, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		var task taskRecord
		err := db.tasks().FindOne(ctx, bson.M{"id": id}).Decode(&task)
		if err == mongo.ErrNoDocuments {
			return nil, tes.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if !server.GetUser(ctx).IsAccessible(task.Owner) {
			return nil, tes.ErrNotPermitted
		}
		if err := tes.ValidateTransition(task.Task.State, target); err != nil {
			return nil, err
		}
		if task.ClaimOwner != "" && tes.TerminalState(task.Task.State) && !tes.TerminalState(target) {
			return nil, fmt.Errorf("task %q has a finished allocation; submit a new task to retry", id)
		}
		if tes.TerminalState(target) && task.ClaimActive {
			if task.ClaimOwner == "" || task.ClaimResources == nil {
				return nil, fmt.Errorf("active task %q has no reservation snapshot or owner", id)
			}
			var node nodeRecord
			err := db.nodes().FindOne(ctx, bson.M{"id": task.ClaimOwner}, options.FindOne().SetCollation(&options.Collation{Locale: "simple"})).Decode(&node)
			if err != nil && err != mongo.ErrNoDocuments {
				return nil, err
			}
			if err == nil {
				node.Reserved, err = node.Reserved.Release(*task.ClaimResources)
				if err != nil {
					return nil, err
				}
				if err := db.saveReservedNode(ctx, &node); err != nil {
					return nil, err
				}
			}
			// Retire the allocation even when its node has disappeared.
			task.ClaimActive = false
		}
		_, err = db.tasks().UpdateOne(ctx, bson.M{"id": id}, bson.M{
			"$set": bson.M{"state": target, "claim_active": task.ClaimActive}, "$inc": bson.M{"version": 1},
		})
		return nil, err
	})
	return err
}
