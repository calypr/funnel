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

	opts := options.Find().SetSort(bson.D{{Key: "creationtime", Value: 1}, {Key: "id", Value: 1}}).SetLimit(int64(n))
	query := bson.M{"state": tes.State_QUEUED}
	if after != nil {
		query["$or"] = bson.A{bson.M{"creationtime": bson.M{"$gt": after.CreationTime}}, bson.M{"creationtime": after.CreationTime, "id": bson.M{"$gt": after.Id}}}
	}
	cursor, err := db.tasks().Find(ctx, query, opts)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	defer cursor.Close(ctx)
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

	var node scheduler.Node
	err := db.nodes().FindOne(mctx, bson.M{"id": req.Id}, options.FindOne().SetCollation(&options.Collation{Locale: "simple"})).Decode(&node)
	if err == mongo.ErrNoDocuments {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err, req.Id)
	}

	if err != nil {
		return nil, err
	}
	if err := db.overlayHeartbeats(mctx, []*scheduler.Node{&node}); err != nil {
		return nil, err
	}
	return &node, nil
}

// DeleteNode deletes a node
func (db *MongoDB) DeleteNode(ctx context.Context, req *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	return &scheduler.DeleteNodeResponse{}, db.deleteNode(ctx, req)
}

// ListNodes is an API endpoint that returns a list of nodes.
func (db *MongoDB) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
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
	findOptions := options.Find().
		SetSort(bson.D{{Key: "id", Value: 1}}).
		SetLimit(int64(page.Limit + 1)).
		SetCollation(&options.Collation{Locale: "simple"})
	var nodes []*scheduler.Node
	cursor, err := db.nodes().Find(mctx, filter, findOptions)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(mctx)

	err = cursor.All(mctx, &nodes)
	if err != nil {
		return nil, err
	}
	if err := db.overlayHeartbeats(mctx, nodes); err != nil {
		return nil, err
	}
	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
