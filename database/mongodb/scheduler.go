package mongodb

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (db *MongoDB) ReadQueue(n int, after *tes.Task) []*tes.Task {
	ctx, cancel := db.context()
	defer cancel()

	opts := options.Find().
		SetProjection(bson.D{{Key: "_id", Value: 0}, {Key: "id", Value: 1}, {Key: "creationtime", Value: 1}, {Key: "state", Value: 1}, {Key: "resources", Value: 1}}).
		SetSort(bson.D{{Key: "creationtime", Value: 1}, {Key: "id", Value: 1}}).
		SetLimit(int64(n))
	query := bson.M{"state": tes.State_QUEUED}
	if after != nil {
		query["$or"] = bson.A{bson.M{"creationtime": bson.M{"$gt": after.CreationTime}}, bson.M{"creationtime": after.CreationTime, "id": bson.M{"$gt": after.Id}}}
	}
	cursor, err := db.tasks().Find(ctx, query, opts)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	defer cursor.Close(ctx) //nolint:errcheck // Cursor cleanup cannot change the fully materialized read-only result.
	var tasks []*tes.Task
	err = cursor.All(ctx, &tasks)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	return tasks
}

// PutNode is an RPC endpoint that is used by nodes to send heartbeats
// and status updates, such as completed tasks. The server responds with updated
// information for the node, such as canceled tasks.
func (db *MongoDB) PutNode(ctx context.Context, node *scheduler.Node) (*scheduler.PutNodeResponse, error) {
	return &scheduler.PutNodeResponse{}, db.putReportedNode(ctx, node)
}

// GetNode gets a node
func (db *MongoDB) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	var record nodeRecord
	err := db.nodes().FindOne(mctx, bson.M{"id": req.Id}, options.FindOne().SetProjection(bson.M{"taskids": 0, "task_ids": 0, "taskIds": 0}).SetCollation(&options.Collation{Locale: "simple"})).Decode(&record)
	if err == mongo.ErrNoDocuments {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err, req.Id)
	}

	if err != nil {
		return nil, err
	}
	nodes, err := db.hydrateNodeRecords(mctx, []*nodeRecord{&record}, true)
	if err != nil {
		return nil, err
	}
	if err := db.overlayHeartbeats(mctx, nodes); err != nil {
		return nil, err
	}
	return nodes[0], nil
}

// DeleteNode deletes a node
func (db *MongoDB) DeleteNode(ctx context.Context, req *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	return &scheduler.DeleteNodeResponse{}, db.deleteNode(ctx, req)
}

// ListNodes is an API endpoint that returns a list of nodes.
func (db *MongoDB) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return db.listNodes(ctx, req, true)
}

// ListNodeCandidates returns scheduling data without expanding each node's active task IDs.
func (db *MongoDB) ListNodeCandidates(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return db.listNodes(ctx, req, false)
}

func (db *MongoDB) listNodes(ctx context.Context, req *scheduler.ListNodesRequest, includeTaskIDs bool) (*scheduler.ListNodesResponse, error) {
	page, err := scheduler.ParseNodePage(req)
	if err != nil {
		return nil, err
	}
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	filter := bson.M{}
	if page.AfterID != "" {
		filter["id"] = bson.M{"$gt": page.AfterID}
	}
	projection := bson.M{"taskids": 0, "task_ids": 0, "taskIds": 0}
	if !includeTaskIDs {
		projection = bson.M{"id": 1, "state": 1, "resources": 1, "available": 1, "preemptible": 1, "zone": 1, "hostname": 1, "version": 1, "metadata": 1, "reserved": 1}
	}
	findOptions := options.Find().
		SetSort(bson.D{{Key: "id", Value: 1}}).
		SetLimit(int64(page.Limit + 1)).
		SetProjection(projection).
		SetCollation(&options.Collation{Locale: "simple"})
	var records []*nodeRecord
	cursor, err := db.nodes().Find(mctx, filter, findOptions)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(mctx) //nolint:errcheck // Cursor cleanup cannot change the fully materialized read-only result.

	err = cursor.All(mctx, &records)
	if err != nil {
		return nil, err
	}
	nodes, err := db.hydrateNodeRecords(mctx, records, includeTaskIDs)
	if err != nil {
		return nil, err
	}
	if err := db.overlayHeartbeats(mctx, nodes); err != nil {
		return nil, err
	}
	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
