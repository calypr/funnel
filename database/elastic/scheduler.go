package elastic

import (
	"context"
	"fmt"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/refresh"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/result"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/sortorder"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	readQueueQuery *types.Query = &types.Query{
		Bool: &types.BoolQuery{
			Filter: []types.Query{
				{
					Term: map[string]types.TermQuery{
						"state": {Value: tes.State_QUEUED.String()},
					},
				},
			},
		},
	}
	readQueueSort types.SortOptions = types.SortOptions{
		SortOptions: map[string]types.FieldSort{
			"id": {Order: &sortorder.Asc},
		},
	}
)

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (es *Elastic) ReadQueue(n int, after *tes.Task) []*tes.Task {
	search := es.client.Search().Index(es.taskIndex).
		Query(readQueueQuery).
		SourceExcludes_(basicExclude...).
		Size(n).
		Sort(readQueueSort)
	if after != nil {
		search = search.SearchAfter(after.Id)
	}
	res, err := search.Do(context.Background())
	if err != nil {
		fmt.Println(err)
		return nil
	}

	var tasks []*tes.Task
	for _, hit := range res.Hits.Hits {
		task := &tes.Task{}
		if err := customJson.Unmarshal(hit.Source_, task); err == nil {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// GetNode gets a node
func (es *Elastic) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	res, err := es.client.Get(es.nodeIndex, req.Id).Do(ctx)

	if !res.Found {
		return nil, status.Errorf(codes.NotFound, "%v: nodeID: %s", err.Error(), req.Id)
	}
	if err != nil {
		return nil, err
	}

	node := &scheduler.Node{}
	err = customJson.Unmarshal(res.Source_, node)
	if err != nil {
		return nil, err
	}
	// Must happen after the unmarshal
	node.Version = *res.Version_
	return node, nil
}

// PutNode puts a node in the database.
//
// For optimisic locking, if the node already exists and node.Version
// doesn't match the version in the database, an error is returned.
func (es *Elastic) PutNode(ctx context.Context, node *scheduler.Node) (*scheduler.PutNodeResponse, error) {
	g := es.client.Get(es.nodeIndex, node.Id).Preference("_primary")

	// If the version is 0, then this should be creating a new node.
	if node.GetVersion() != 0 {
		v := node.GetVersion()
		g.Version(int64ToStr(&v))
	}

	res, err := g.Do(ctx)
	if err != nil {
		return nil, err
	}

	existing := &scheduler.Node{}
	err = customJson.Unmarshal(res.Source_, existing)
	if err != nil {
		return nil, err
	}

	err = scheduler.UpdateNode(ctx, es, node, existing)
	if err != nil {
		return nil, err
	}

	i := es.client.Index(es.nodeIndex).
		Id(node.Id).
		Refresh(refresh.True).
		Document(node)

	if node.GetVersion() != 0 {
		v := node.GetVersion()
		i = i.Version(int64ToStr(&v))
	}
	resp, err := i.Do(ctx)
	if resp.Result != result.Created && resp.Result != result.Updated {
		return nil, fmt.Errorf(
			"Node [%s] was not recorded in ElasticSearch; response was: %s",
			node.Id, resp.Result)
	}

	return &scheduler.PutNodeResponse{}, err
}

// DeleteNode deletes a node by ID.
func (es *Elastic) DeleteNode(ctx context.Context, node *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	_, err := es.client.Delete(es.nodeIndex, node.Id).
		Version(int64ToStr(&node.Version)).
		Refresh(refresh.True).
		Do(ctx)
	return &scheduler.DeleteNodeResponse{}, err
}

// ListNodes is an API endpoint that returns a list of nodes.
func (es *Elastic) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	res, err := es.client.Search().
		Index(es.nodeIndex).
		Version(true).
		Size(1000).
		Do(ctx)

	if err != nil {
		return nil, err
	}

	resp := &scheduler.ListNodesResponse{}
	for _, hit := range res.Hits.Hits {
		node := &scheduler.Node{}
		err = customJson.Unmarshal(hit.Source_, node)
		if err != nil {
			return nil, err
		}
		node.Version = *hit.Version_
		resp.Nodes = append(resp.Nodes, node)
	}

	return resp, nil
}
