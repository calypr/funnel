package mongodb

import (
	"context"

	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type stateCount struct {
	State tes.State `bson:"_id"`
	Count int       `bson:"count"`
}

// TaskStateCounts returns the number of tasks in each state.
func (db *MongoDB) TaskStateCounts(ctx context.Context) (map[string]int32, error) {
	stateStage := bson.D{{
		Key: "$sort", Value: bson.D{{Key: "state", Value: 1}},
	}}

	groupStage := bson.D{
		{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$state"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		},
		}}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	cursor, err := db.tasks().Aggregate(mctx, mongo.Pipeline{stateStage, groupStage})
	if err != nil {
		return nil, err
	}

	mctx, cancel = db.wrap(ctx)
	defer cancel()

	recs := []stateCount{}
	err = cursor.All(mctx, &recs)
	if err != nil {
		return nil, err
	}

	counts := map[string]int32{}
	for _, rec := range recs {
		counts[rec.State.String()] = int32(rec.Count)
	}

	return counts, nil
}
