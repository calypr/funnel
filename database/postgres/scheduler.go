package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (db *Postgres) ReadQueue(n int, after *tes.Task) []*tes.Task {
	ctx, cancel := db.context()
	defer cancel()

	const firstPageSQL = `
		SELECT id, creation_time, state, data->'resources'
		FROM tasks
		WHERE state = 'QUEUED'
		ORDER BY creation_time ASC, id ASC
		LIMIT $1
	`
	const nextPageSQL = `
		SELECT id, creation_time, state, data->'resources'
		FROM tasks
		WHERE state = 'QUEUED'
		AND (creation_time, id) > ($1::timestamptz, $2::varchar)
		ORDER BY creation_time ASC, id ASC
		LIMIT $3
	`
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = db.client.Query(ctx, firstPageSQL, n)
	} else {
		rows, err = db.client.Query(ctx, nextPageSQL, after.CreationTime, after.Id, n)
	}
	if err != nil {
		fmt.Println("Error reading queue:", err)
		return nil
	}
	defer rows.Close()

	var tasks []*tes.Task
	for rows.Next() {
		var task tes.Task
		var creationTime time.Time
		var state string
		var resourcesJSON []byte
		if err := rows.Scan(&task.Id, &creationTime, &state, &resourcesJSON); err != nil {
			fmt.Println("Error scanning task row:", err)
			continue
		}
		task.CreationTime = creationTime.Format(time.RFC3339Nano)
		task.State = tes.State(tes.State_value[state])
		if len(resourcesJSON) > 0 && string(resourcesJSON) != "null" {
			if err := json.Unmarshal(resourcesJSON, &task.Resources); err != nil {
				fmt.Println("Error unmarshaling task resources:", err)
				continue
			}
		}
		tasks = append(tasks, &task)
	}

	if rows.Err() != nil {
		fmt.Println("Error iterating task rows:", rows.Err())
	}

	return tasks
}

// PutNode is an RPC endpoint that is used by nodes to send heartbeats and status updates.
func (db *Postgres) PutNode(ctx context.Context, node *scheduler.Node) (*scheduler.PutNodeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()
	if err := db.putReportedNode(ctx, node); err != nil {
		return nil, err
	}
	return &scheduler.PutNodeResponse{}, nil
}

// GetNode gets a node
func (db *Postgres) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	ctx, cancel := db.context()
	defer cancel()

	selectSQL := `SELECT n.data,n.state,n.version,n.reserved,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id WHERE n.id=$1`
	var stamp int64
	var nodeJSON []byte
	var reservedJSON []byte
	var nodeState string
	var version int64

	err := db.client.QueryRow(ctx, selectSQL, req.Id).Scan(&nodeJSON, &nodeState, &version, &reservedJSON, &stamp)
	if err == pgx.ErrNoRows {
		return nil, status.Errorf(codes.NotFound, "nodeID: %s not found", req.Id)
	}
	if err != nil {
		return nil, err
	}

	var node scheduler.Node
	if err := json.Unmarshal(nodeJSON, &node); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node JSON: %w", err)
	}
	state, ok := scheduler.NodeState_value[nodeState]
	if !ok {
		return nil, fmt.Errorf("node %q has invalid state %q", req.Id, nodeState)
	}
	node.State = scheduler.NodeState(state)

	if err := hydrateNodeProjection(&node, version, reservedJSON, nil); err != nil {
		return nil, err
	}
	taskIDs, err := db.activeTaskIDs(ctx, []string{node.Id})
	if err != nil {
		return nil, err
	}
	node.TaskIds = taskIDs[node.Id]
	node.LastPing = stamp
	return &node, nil
}

// DeleteNode deletes a node
func (db *Postgres) DeleteNode(ctx context.Context, req *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	ctx, cancel := db.context()
	defer cancel()

	tx, err := db.client.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var stamp int64
	err = tx.QueryRow(ctx, `SELECT last_ping FROM node_heartbeats WHERE id=$1 FOR UPDATE`, req.Id).Scan(&stamp)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	var version int64
	var nodeState string
	err = tx.QueryRow(ctx, `SELECT version,state FROM nodes WHERE id=$1 FOR UPDATE`, req.Id).Scan(&version, &nodeState)
	if err == pgx.ErrNoRows {
		return nil, status.Errorf(codes.NotFound, "nodeID: %s not found", req.Id)
	}
	if err != nil {
		return nil, err
	}
	state, ok := scheduler.NodeState_value[nodeState]
	if !ok {
		return nil, fmt.Errorf("node %q has invalid state %q", req.Id, nodeState)
	}
	currentState := scheduler.NodeState(state)
	finishedCleanup := false
	if req.State == scheduler.NodeState_GONE && currentState == scheduler.NodeState_GONE {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE claim_owner=$1 AND claim_active)`, req.Id).Scan(&active); err != nil {
			return nil, err
		}
		finishedCleanup = !active
	}
	if req.Version != 0 && ((version != req.Version && !finishedCleanup) || stamp != req.LastPing) {
		return nil, scheduler.ErrNodeChanged
	}

	if _, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, req.Id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM node_heartbeats WHERE id=$1`, req.Id); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &scheduler.DeleteNodeResponse{}, nil
}

// ListNodes is an API endpoint that returns a list of nodes.
func (db *Postgres) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return db.listNodes(ctx, req, true)
}

// ListNodeCandidates returns scheduling data without expanding each node's active task IDs.
func (db *Postgres) ListNodeCandidates(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	return db.listNodes(ctx, req, false)
}

func (db *Postgres) listNodes(ctx context.Context, req *scheduler.ListNodesRequest, includeTaskIDs bool) (*scheduler.ListNodesResponse, error) {
	page, err := scheduler.ParseNodePage(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()

	var rows pgx.Rows
	selectNode := `n.data`
	if !includeTaskIDs {
		selectNode = `jsonb_build_object(
			'id', n.id,
			'resources', n.data->'resources',
			'preemptible', n.data->'preemptible',
			'zone', n.data->'zone',
			'hostname', n.data->'hostname',
			'metadata', n.data->'metadata'
		)`
	}
	if page.AfterID == "" {
		rows, err = db.client.Query(ctx, `SELECT `+selectNode+`,n.state,n.version,n.reserved,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id ORDER BY n.id ASC LIMIT $1`, page.Limit+1)
	} else {
		rows, err = db.client.Query(ctx, `SELECT `+selectNode+`,n.state,n.version,n.reserved,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id WHERE n.id > $1 ORDER BY n.id ASC LIMIT $2`, page.AfterID, page.Limit+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*scheduler.Node
	var versions []int64
	var states []string
	var reservedResources [][]byte
	var stamps []int64
	for rows.Next() {
		var nodeJSON []byte
		var reservedJSON []byte
		var version, stamp int64
		var state string
		if err := rows.Scan(&nodeJSON, &state, &version, &reservedJSON, &stamp); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}

		var node scheduler.Node
		if err := json.Unmarshal(nodeJSON, &node); err != nil {
			return nil, fmt.Errorf("unmarshaling node JSON: %w", err)
		}
		nodes = append(nodes, &node)
		states = append(states, state)
		versions = append(versions, version)
		reservedResources = append(reservedResources, reservedJSON)
		stamps = append(stamps, stamp)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	owners := make([]string, len(nodes))
	for i, node := range nodes {
		state, ok := scheduler.NodeState_value[states[i]]
		if !ok {
			return nil, fmt.Errorf("node %q has invalid state %q", node.Id, states[i])
		}
		node.State = scheduler.NodeState(state)
		if err := hydrateNodeProjection(node, versions[i], reservedResources[i], nil); err != nil {
			return nil, err
		}
		node.LastPing = stamps[i]
		owners[i] = node.Id
	}
	if includeTaskIDs {
		taskIDs, err := db.activeTaskIDs(ctx, owners)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			node.TaskIds = taskIDs[node.Id]
		}
	}
	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
