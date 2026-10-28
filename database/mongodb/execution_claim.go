package mongodb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (db *MongoDB) migrateExecutionClaims(ctx context.Context) error {
	const migration = "execution-claims-v1"
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
		_, err := db.tasks().UpdateMany(ctx, bson.M{
			"state": bson.M{"$in": []tes.State{tes.State_RUNNING, tes.State_COMPLETE, tes.State_CANCELED, tes.State_EXECUTOR_ERROR, tes.State_SYSTEM_ERROR}},
		}, bson.M{"$set": bson.M{"execution_claimed": true}})
		if err != nil {
			return nil, err
		}
		_, err = migrations.UpdateOne(ctx, bson.M{"_id": migration}, bson.M{"$set": bson.M{"done": true}})
		return nil, err
	})
	return err
}

// ClaimExecution atomically admits one worker for a queued or initializing task.
func (db *MongoDB) ClaimExecution(ctx context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	var ownerMetadata map[string]string
	if owner != nil {
		var err error
		ownerMetadata, err = events.ExecutionOwnerMetadata(owner)
		if err != nil {
			return false, err
		}
	}
	result, err := db.transaction(ctx, func(ctx context.Context) (any, error) {
		var stored taskRecord
		err := db.tasks().FindOne(ctx, bson.M{"id": taskID}, options.FindOne().SetProjection(bson.M{
			"id": 1, "owner": 1, "state": 1, "execution_claimed": 1,
		})).Decode(&stored)
		if err == mongo.ErrNoDocuments {
			return nil, tes.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if !server.GetUser(ctx).IsAccessible(stored.Owner) {
			return nil, tes.ErrNotPermitted
		}
		if stored.ExecutionClaimed || (stored.Task.GetState() != tes.State_QUEUED && stored.Task.GetState() != tes.State_INITIALIZING) {
			return false, nil
		}
		filter := bson.M{
			"id":                taskID,
			"owner":             stored.Owner,
			"state":             stored.Task.GetState(),
			"execution_claimed": bson.M{"$ne": true},
		}
		set := bson.M{"state": tes.State_INITIALIZING, "execution_claimed": true}
		if ownerMetadata != nil {
			set["logs.0.metadata."+events.ExecutionOwnerMetadataKey] = ownerMetadata[events.ExecutionOwnerMetadataKey]
		}
		update := bson.M{
			"$set": set,
			"$inc": bson.M{"version": 1},
		}
		updated, err := db.tasks().UpdateOne(ctx, filter, update)
		if err != nil {
			return nil, err
		}
		if updated.MatchedCount != 1 {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	claimed, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("claim execution returned unexpected result %T", result)
	}
	return claimed, nil
}
