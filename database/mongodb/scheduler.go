package mongodb

import (
	"fmt"

	"context"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (db *MongoDB) ReadQueue(n int) []*tes.Task {
	ctx, cancel := db.context()
	defer cancel()

	fmt.Println("Reading queue!")
	opts := options.Find().SetSort(bson.M{"creationtime": 1}).SetLimit(int64(n))
	cursor, err := db.tasks().Find(ctx, bson.M{"state": tes.State_QUEUED}, opts)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	ctx, cancel = db.context()
	defer cancel()

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
	nodes := db.nodes()

	q := bson.M{"id": node.Id}

	if node.GetVersion() != 0 {
		q["version"] = node.GetVersion()
	}

	mctx, cancel := db.wrap(ctx)
	defer cancel()

	var existing scheduler.Node
	err := nodes.FindOne(mctx, bson.M{"id": node.Id}).Decode(&existing)
	if err != nil {
		return nil, err
	}

	mctx, cancel = db.wrap(ctx)
	defer cancel()

	// Warm the connection pool; the result is intentionally unused.
	_, _ = db.GetTask(ctx, &tes.GetTaskRequest{Id: "foo"})
	err = scheduler.UpdateNode(mctx, db, node, &existing)
	if err != nil {
		return nil, err
	}

	node.Version = node.GetVersion() + 1

	mctx, cancel = db.wrap(ctx)
	defer cancel()

	opts := options.UpdateOne().SetUpsert(true)
	_, err = nodes.UpdateOne(mctx, q, node, opts)

	return &scheduler.PutNodeResponse{}, err
}

// GetNode gets a node
func (db *MongoDB) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	var node scheduler.Node
	err := db.nodes().FindOne(mctx, bson.M{"id": req.Id}).Decode(&node)
	if err == mongo.ErrNoDocuments {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err, req.Id)
	}

	return &node, nil
}

// DeleteNode deletes a node
func (db *MongoDB) DeleteNode(ctx context.Context, req *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	fmt.Println("DeleteNode", req.Id)
	_, err := db.nodes().DeleteOne(mctx, bson.M{"id": req.Id})
	fmt.Println("DeleteNode", req.Id, err)
	if err == mongo.ErrNoDocuments {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err, req.Id)
	}
	return nil, err
}

// ListNodes is an API endpoint that returns a list of nodes.
func (db *MongoDB) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	mctx, cancel := db.wrap(ctx)
	defer cancel()

	var nodes []*scheduler.Node
	cursor, err := db.nodes().Find(mctx, nil)
	if err != nil {
		return nil, err
	}

	err = cursor.All(mctx, &nodes)
	if err != nil {
		return nil, err
	}
	out := &scheduler.ListNodesResponse{
		Nodes: nodes,
	}

	return out, nil
}
