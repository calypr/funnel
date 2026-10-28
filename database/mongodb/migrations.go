package mongodb

import (
	"context"
	"fmt"
	"slices"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (db *MongoDB) migrateScheduler(ctx context.Context) error {
	migrations := db.collection("scheduler_migrations")
	const migration = "transactional-reservations-v1"
	// Create the serialization document before entering the transaction. Concurrent
	// initializers update it to serialize the one-time import of legacy task lists.
	if _, err := migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$setOnInsert": bson.M{"done": false}}, options.UpdateOne().SetUpsert(true)); err != nil {
		return err
	}
	_, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		var marker struct {
			Done bool `bson:"done"`
		}
		if err := migrations.FindOne(ctx, bson.M{"_id": migration}).Decode(&marker); err != nil {
			return nil, err
		}
		if marker.Done {
			return nil, nil
		}
		if _, err := migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$inc": bson.M{"revision": 1}}); err != nil {
			return nil, err
		}
		cursor, err := db.nodes().Find(ctx, bson.M{})
		if err != nil {
			return nil, err
		}
		var nodes []nodeRecord
		if err := cursor.All(ctx, &nodes); err != nil {
			return nil, err
		}
		owners := make(map[string]string)
		for i := range nodes {
			node := &nodes[i]
			oldIDs := node.Node.TaskIds
			node.Node.TaskIds = nil
			node.Reserved = scheduler.ResourceUsage{}
			for _, id := range oldIDs {
				var task taskRecord
				err := db.tasks().FindOne(ctx, bson.M{"id": id}).Decode(&task)
				if err == mongo.ErrNoDocuments {
					continue
				}
				if err != nil {
					return nil, err
				}
				if tes.TerminalState(task.Task.State) {
					continue
				}
				if owner := owners[id]; owner != "" && owner != node.Node.Id {
					return nil, fmt.Errorf("task %q appears on multiple nodes (%s, %s); resolve conflicting assignments before startup", id, owner, node.Node.Id)
				}
				if task.ClaimOwner != "" && task.ClaimOwner != node.Node.Id {
					return nil, fmt.Errorf("task %q has a conflicting allocation owner", id)
				}
				owners[id] = node.Node.Id
				if slices.Contains(node.Node.TaskIds, id) {
					continue
				}
				held, err := scheduler.TaskResourceUsage(task.Task)
				if err != nil {
					return nil, err
				}
				node.Reserved, err = node.Reserved.Add(held)
				if err != nil {
					return nil, err
				}
				node.Node.TaskIds = append(node.Node.TaskIds, id)
				if _, err := db.tasks().UpdateOne(ctx, bson.M{"id": id}, bson.M{"$set": bson.M{
					"claim_owner": node.Node.Id, "claim_active": true, "claim_resources": held,
				}}); err != nil {
					return nil, err
				}
			}
			if err := db.saveReservedNode(ctx, node); err != nil {
				return nil, err
			}
			if _, err := db.heartbeats().UpdateOne(ctx, bson.M{"_id": node.Node.Id},
				bson.M{"$setOnInsert": bson.M{"last_ping": node.Node.LastPing}}, options.UpdateOne().SetUpsert(true)); err != nil {
				return nil, err
			}
		}
		_, err = migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$set": bson.M{"done": true}})
		return nil, err
	})
	return err
}

func (db *MongoDB) migrateNodeTaskIDProjection(ctx context.Context) error {
	const migration = "node-taskids-projection-v1"
	migrations := db.collection("scheduler_migrations")
	if _, err := migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$setOnInsert": bson.M{"done": false}}, options.UpdateOne().SetUpsert(true)); err != nil {
		return err
	}
	_, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		var marker struct {
			Done bool `bson:"done"`
		}
		if err := migrations.FindOne(ctx, bson.M{"_id": migration}).Decode(&marker); err != nil {
			return nil, err
		}
		if marker.Done {
			return nil, nil
		}
		if _, err := migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$inc": bson.M{"revision": 1}}); err != nil {
			return nil, err
		}
		_, err := db.nodes().UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{"taskids": "", "task_ids": "", "taskIds": ""}})
		if err != nil {
			return nil, err
		}
		_, err = migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$set": bson.M{"done": true}})
		return nil, err
	})
	return err
}

func (db *MongoDB) requireTransactions(ctx context.Context) error {
	var hello struct {
		SetName string `bson:"setName"`
		Message string `bson:"msg"`
	}
	if err := db.client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return err
	}
	if hello.SetName == "" && hello.Message != "isdbgrid" {
		return fmt.Errorf("MongoDB requires a replica set or sharded cluster for atomic task assignment; standalone MongoDB is not supported")
	}
	return nil
}
