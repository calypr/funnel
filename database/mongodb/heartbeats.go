package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nodeHeartbeat struct {
	ID       string `bson:"_id"`
	LastPing int64  `bson:"last_ping"`
}

func (db *MongoDB) heartbeats() *mongo.Collection { return db.collection("node_heartbeats") }

func (db *MongoDB) putReportedNode(ctx context.Context, report *scheduler.Node) error {
	if report == nil || report.Id == "" {
		return fmt.Errorf("node ID is required")
	}
	if err := scheduler.ValidateNodeCapacity(report.Resources); err != nil {
		return err
	}
	result, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		var heartbeat nodeHeartbeat
		err := db.heartbeats().FindOne(ctx, bson.M{"_id": report.Id}).Decode(&heartbeat)
		if err != nil && err != mongo.ErrNoDocuments {
			return nil, err
		}
		stamp := max(time.Now().UnixNano(), heartbeat.LastPing+1)
		// Health changes and deletion also write this document. A heartbeat that
		// races either operation retries against its committed node state.
		if _, err := db.heartbeats().UpdateOne(ctx, bson.M{"_id": report.Id},
			bson.M{"$set": bson.M{"last_ping": stamp}}, options.UpdateOne().SetUpsert(true)); err != nil {
			return nil, err
		}
		var stored nodeRecord
		err = db.nodes().FindOne(ctx, bson.M{"id": report.Id}, options.FindOne().SetCollation(&options.Collation{Locale: "simple"})).Decode(&stored)
		isNew := err == mongo.ErrNoDocuments
		if err != nil && !isNew {
			return nil, err
		}
		if isNew {
			count, err := db.tasks().CountDocuments(ctx, bson.M{"claim_owner": report.Id, "claim_active": true}, options.Count().SetLimit(1))
			if err != nil {
				return nil, err
			}
			if count != 0 {
				return nil, fmt.Errorf("node ID %q still owns active allocations; register with a new ID", report.Id)
			}
		} else if stored.Node.State == scheduler.NodeState_GONE && report.State != scheduler.NodeState_GONE {
			return nil, fmt.Errorf("gone node %q cannot re-register with the same ID", report.Id)
		}
		if isNew || scheduler.NodeReportChanged(report, &stored.Node) {
			if report.Version != stored.Node.Version {
				return nil, scheduler.ErrNodeChanged
			}
			stored.Node.Id = report.Id
			scheduler.ApplyNodeReport(&stored.Node, report)
			if isNew {
				stored.Node.Version = 1
				stored.Node.Available = stored.Reserved.Available(stored.Node.Resources)
				_, err = db.nodes().InsertOne(ctx, &stored)
			} else {
				err = db.saveReservedNode(ctx, &stored)
			}
			if err != nil {
				return nil, err
			}
		}
		stored.Node.LastPing = stamp
		return &stored.Node, nil
	})
	if err != nil {
		return err
	}
	current := result.(*scheduler.Node)
	report.Version, report.LastPing = current.Version, current.LastPing
	report.TaskIds, report.Available = current.TaskIds, current.Available
	return nil
}

// fenceHeartbeat participates in the transaction's write-conflict detection.
// A snapshot read alone would allow a stale health change to race a heartbeat.
func (db *MongoDB) fenceHeartbeat(ctx context.Context, observed *scheduler.Node) error {
	result, err := db.heartbeats().UpdateOne(ctx,
		bson.M{"_id": observed.Id, "last_ping": observed.LastPing}, bson.M{"$inc": bson.M{"fence": 1}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return scheduler.ErrNodeChanged
	}
	return nil
}

func (db *MongoDB) SetNodeState(ctx context.Context, observed *scheduler.Node) (*scheduler.Node, error) {
	result, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		if err := db.fenceHeartbeat(ctx, observed); err != nil {
			return nil, err
		}
		var stored nodeRecord
		err := db.nodes().FindOne(ctx, bson.M{"id": observed.Id}, options.FindOne().SetCollation(&options.Collation{Locale: "simple"})).Decode(&stored)
		if err == mongo.ErrNoDocuments {
			return nil, scheduler.ErrNodeChanged
		}
		if err != nil {
			return nil, err
		}
		if stored.Node.Version != observed.Version {
			return nil, scheduler.ErrNodeChanged
		}
		if stored.Node.State != observed.State {
			stored.Node.State = observed.State
			if err := db.saveReservedNode(ctx, &stored); err != nil {
				return nil, err
			}
		}
		stored.Node.LastPing = observed.LastPing
		return &stored.Node, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*scheduler.Node), nil
}

func (db *MongoDB) deleteNode(ctx context.Context, observed *scheduler.Node) error {
	_, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		if observed.Version != 0 {
			if err := db.fenceHeartbeat(ctx, observed); err != nil {
				return nil, err
			}
		} else {
			if _, err := db.heartbeats().DeleteOne(ctx, bson.M{"_id": observed.Id}); err != nil {
				return nil, err
			}
		}
		var stored nodeRecord
		err := db.nodes().FindOne(ctx, bson.M{"id": observed.Id}, options.FindOne().SetCollation(&options.Collation{Locale: "simple"})).Decode(&stored)
		if err == mongo.ErrNoDocuments {
			return nil, status.Error(codes.NotFound, "node not found")
		}
		if err != nil {
			return nil, err
		}
		safeCleanup := observed.State == scheduler.NodeState_GONE && stored.Node.State == scheduler.NodeState_GONE && len(stored.Node.TaskIds) == 0
		if observed.Version != 0 && stored.Node.Version != observed.Version && !safeCleanup {
			return nil, scheduler.ErrNodeChanged
		}
		if _, err := db.nodes().DeleteOne(ctx, bson.M{"id": observed.Id}, options.DeleteOne().SetCollation(&options.Collation{Locale: "simple"})); err != nil {
			return nil, err
		}
		_, err = db.heartbeats().DeleteOne(ctx, bson.M{"_id": observed.Id})
		return nil, err
	})
	return err
}

func (db *MongoDB) overlayHeartbeats(ctx context.Context, nodes []*scheduler.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ids := make([]string, len(nodes))
	for i, node := range nodes {
		ids[i] = node.Id
	}
	cursor, err := db.heartbeats().Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	stamps := make(map[string]int64, len(nodes))
	for cursor.Next(ctx) {
		var heartbeat nodeHeartbeat
		if err := cursor.Decode(&heartbeat); err != nil {
			return err
		}
		stamps[heartbeat.ID] = heartbeat.LastPing
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	for _, node := range nodes {
		node.LastPing = stamps[node.Id]
	}
	return nil
}
