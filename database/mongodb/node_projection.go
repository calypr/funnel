package mongodb

import (
	"context"
	"sort"

	"github.com/calypr/funnel/compute/scheduler"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type activeTaskOwner struct {
	ID    string `bson:"id"`
	Owner string `bson:"claim_owner"`
}

func (db *MongoDB) activeTaskIDs(ctx context.Context, nodeIDs []string) (map[string][]string, error) {
	ids := make(map[string][]string, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return ids, nil
	}
	cursor, err := db.tasks().Find(ctx,
		bson.M{"claim_owner": bson.M{"$in": nodeIDs}, "claim_active": true},
		options.Find().SetProjection(bson.M{"id": 1, "claim_owner": 1}).SetSort(bson.D{{Key: "claim_owner", Value: 1}, {Key: "id", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx) //nolint:errcheck // Cursor cleanup cannot change results; decode and iteration errors are returned below.
	for cursor.Next(ctx) {
		var claim activeTaskOwner
		if err := cursor.Decode(&claim); err != nil {
			return nil, err
		}
		ids[claim.Owner] = append(ids[claim.Owner], claim.ID)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	for owner := range ids {
		sort.Strings(ids[owner])
	}
	return ids, nil
}

func (db *MongoDB) hydrateNodeRecords(ctx context.Context, records []*nodeRecord, includeTaskIDs bool) ([]*scheduler.Node, error) {
	owners := make([]string, len(records))
	for i, record := range records {
		owners[i] = record.Node.Id
	}
	var activeIDs map[string][]string
	if includeTaskIDs {
		var err error
		activeIDs, err = db.activeTaskIDs(ctx, owners)
		if err != nil {
			return nil, err
		}
	}
	nodes := make([]*scheduler.Node, len(records))
	for i, record := range records {
		node := &record.Node
		node.Available = record.Reserved.Available(node.Resources)
		if includeTaskIDs {
			node.TaskIds = activeIDs[node.Id]
		} else {
			node.TaskIds = nil
		}
		nodes[i] = node
	}
	return nodes, nil
}

func (db *MongoDB) hasActiveTaskIDs(ctx context.Context, nodeID string) (bool, error) {
	count, err := db.tasks().CountDocuments(ctx, bson.M{"claim_owner": nodeID, "claim_active": true}, options.Count().SetLimit(1))
	return count != 0, err
}
