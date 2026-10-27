package postgres

import (
	"context"
	"encoding/json"
	"fmt"

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

	// Select the full JSONB data field, ordering by creation_time (which is indexed)
	// and filtered by state.
	selectSQL := `
		SELECT data 
		FROM tasks 
		WHERE state = $1 
		AND ($3::timestamptz IS NULL OR (creation_time, id) > ($3::timestamptz, $4))
		ORDER BY creation_time ASC, id ASC
		LIMIT $2
	`

	var creationTime any
	var id string
	if after != nil {
		creationTime, id = after.CreationTime, after.Id
	}
	rows, err := db.client.Query(ctx, selectSQL, tes.State_QUEUED.String(), n, creationTime, id)
	if err != nil {
		fmt.Println("Error reading queue:", err)
		return nil
	}
	defer rows.Close()

	var tasks []*tes.Task
	for rows.Next() {
		var taskJSON []byte
		if err := rows.Scan(&taskJSON); err != nil {
			fmt.Println("Error scanning task row:", err)
			continue
		}

		var task tes.Task
		if err := json.Unmarshal(taskJSON, &task); err != nil {
			fmt.Println("Error unmarshaling task JSON:", err)
			continue
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

	selectSQL := `SELECT n.data,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id WHERE n.id=$1`
	var stamp int64
	var nodeJSON []byte

	err := db.client.QueryRow(ctx, selectSQL, req.Id).Scan(&nodeJSON, &stamp)
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
	var data []byte
	err = tx.QueryRow(ctx, `SELECT version,data FROM nodes WHERE id=$1 FOR UPDATE`, req.Id).Scan(&version, &data)
	if err == pgx.ErrNoRows {
		return nil, status.Errorf(codes.NotFound, "nodeID: %s not found", req.Id)
	}
	if err != nil {
		return nil, err
	}
	var current scheduler.Node
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, err
	}
	finishedCleanup := req.State == scheduler.NodeState_GONE && current.State == scheduler.NodeState_GONE && len(current.TaskIds) == 0
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
	page, err := scheduler.ParseNodePage(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()

	var rows pgx.Rows
	if page.AfterID == "" {
		rows, err = db.client.Query(ctx, `SELECT n.data,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id ORDER BY n.id ASC LIMIT $1`, page.Limit+1)
	} else {
		rows, err = db.client.Query(ctx, `SELECT n.data,COALESCE(h.last_ping,0) FROM nodes n LEFT JOIN node_heartbeats h ON h.id=n.id WHERE n.id > $1 ORDER BY n.id ASC LIMIT $2`, page.AfterID, page.Limit+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*scheduler.Node
	for rows.Next() {
		var nodeJSON []byte
		var stamp int64
		if err := rows.Scan(&nodeJSON, &stamp); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}

		var node scheduler.Node
		if err := json.Unmarshal(nodeJSON, &node); err != nil {
			return nil, fmt.Errorf("unmarshaling node JSON: %w", err)
		}
		node.LastPing = stamp
		nodes = append(nodes, &node)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
