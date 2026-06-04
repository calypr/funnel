package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v4"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadQueue returns a slice of queued Tasks. Up to "n" tasks are returned.
func (db *Postgres) ReadQueue(n int) []*tes.Task {
	ctx, cancel := db.context()
	defer cancel()

	// Select the full JSONB data field, ordering by creation_time (which is indexed)
	// and filtered by state.
	selectSQL := `
		SELECT data 
		FROM tasks 
		WHERE state = $1 
		ORDER BY creation_time ASC 
		LIMIT $2
	`

	rows, err := db.client.Query(ctx, selectSQL, tes.State_QUEUED.String(), n)
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
	ctx, cancel := db.context()
	defer cancel()

	// Try to get existing node
	if err := db.client.QueryRow(ctx, "SELECT data FROM nodes WHERE id = $1", node.Id).Scan(&[]byte{}); err != nil {
		if err == pgx.ErrNoRows {
			// Node does not exist, insert it.
			return db.insertNode(ctx, node)
		}
		return nil, err
	}

	// 2. Fetch the full existing node structure (simplified for brevity)
	// NOTE: A robust implementation needs to fetch, update, and persist like TaskState.
	// For now, we update core fields and overwrite JSONB.
	node.Version = node.GetVersion() + 1

	nodeJSON, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal node to JSON: %w", err)
	}

	updateSQL := `
		UPDATE nodes 
		SET state = $1, version = $2, last_heartbeat = $3, data = $4::jsonb
		WHERE id = $5
	`
	_, err = db.client.Exec(ctx, updateSQL,
		node.State.String(),
		node.Version,
		nodeJSON,
		node.Id)

	if err != nil {
		return nil, err
	}

	return &scheduler.PutNodeResponse{}, nil
}

// insertNode inserts a new node into the database.
func (db *Postgres) insertNode(ctx context.Context, node *scheduler.Node) (*scheduler.PutNodeResponse, error) {
	node.Version = 1

	nodeJSON, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal node to JSON: %w", err)
	}

	insertSQL := `
        INSERT INTO nodes (id, state, version, last_heartbeat, data)
        VALUES ($1, $2, $3, $4, $5::jsonb)
    `
	_, err = db.client.Exec(ctx, insertSQL,
		node.Id,
		node.State.String(),
		node.Version,
		nodeJSON)

	return &scheduler.PutNodeResponse{}, err
}

// GetNode gets a node
func (db *Postgres) GetNode(ctx context.Context, req *scheduler.GetNodeRequest) (*scheduler.Node, error) {
	ctx, cancel := db.context()
	defer cancel()

	selectSQL := `SELECT data FROM nodes WHERE id = $1`
	var nodeJSON []byte

	err := db.client.QueryRow(ctx, selectSQL, req.Id).Scan(&nodeJSON)
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

	return &node, nil
}

// DeleteNode deletes a node
func (db *Postgres) DeleteNode(ctx context.Context, req *scheduler.Node) (*scheduler.DeleteNodeResponse, error) {
	ctx, cancel := db.context()
	defer cancel()

	deleteSQL := `DELETE FROM nodes WHERE id = $1`
	tag, err := db.client.Exec(ctx, deleteSQL, req.Id)
	if err != nil {
		return nil, err
	}

	if tag.RowsAffected() == 0 {
		return nil, status.Errorf(codes.NotFound, "nodeID: %s not found", req.Id)
	}

	return &scheduler.DeleteNodeResponse{}, nil
}

// ListNodes is an API endpoint that returns a list of nodes.
func (db *Postgres) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	ctx, cancel := db.context()
	defer cancel()

	selectSQL := `SELECT data FROM nodes`
	rows, err := db.client.Query(ctx, selectSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*scheduler.Node
	for rows.Next() {
		var nodeJSON []byte
		if err := rows.Scan(&nodeJSON); err != nil {
			fmt.Println("Error scanning node row:", err)
			continue
		}

		var node scheduler.Node
		if err := json.Unmarshal(nodeJSON, &node); err != nil {
			fmt.Println("Error unmarshaling node JSON:", err)
			continue
		}
		nodes = append(nodes, &node)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return &scheduler.ListNodesResponse{
		Nodes: nodes,
	}, nil
}
