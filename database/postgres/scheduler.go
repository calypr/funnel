package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	ctx, cancel := db.context()
	defer cancel()

	var data []byte
	var version int64
	err := db.client.QueryRow(ctx, "SELECT data, version FROM nodes WHERE id = $1", node.Id).Scan(&data, &version)
	isNew := err == pgx.ErrNoRows
	if err != nil && !isNew {
		return nil, err
	}
	existing := &scheduler.Node{}
	if !isNew {
		if err := json.Unmarshal(data, existing); err != nil {
			return nil, fmt.Errorf("unmarshaling node %s: %w", node.Id, err)
		}
		if node.Version != version {
			return nil, fmt.Errorf("node %s version outdated", node.Id)
		}
	}
	if err := scheduler.UpdateNode(ctx, db, node, existing); err != nil {
		return nil, err
	}
	node.Version = version + 1
	data, err = json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("marshaling node %s: %w", node.Id, err)
	}
	heartbeat := time.Unix(0, node.LastPing)
	if isNew {
		_, err = db.client.Exec(ctx, `INSERT INTO nodes (id, state, version, last_heartbeat, data) VALUES ($1, $2, $3, $4, $5::jsonb)`, node.Id, node.State.String(), node.Version, heartbeat, data)
	} else {
		var tag pgconn.CommandTag
		tag, err = db.client.Exec(ctx, `UPDATE nodes SET state = $1, version = $2, last_heartbeat = $3, data = $4::jsonb WHERE id = $5 AND version = $6`, node.State.String(), node.Version, heartbeat, data, node.Id, version)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("node %s version outdated", node.Id)
		}
	}
	if err != nil {
		return nil, err
	}
	return &scheduler.PutNodeResponse{}, nil
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
	page, err := scheduler.ParseNodePage(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()

	var rows pgx.Rows
	if page.AfterID == "" {
		rows, err = db.client.Query(ctx, `SELECT data FROM nodes ORDER BY id ASC LIMIT $1`, page.Limit+1)
	} else {
		rows, err = db.client.Query(ctx, `SELECT data FROM nodes WHERE id > $1 ORDER BY id ASC LIMIT $2`, page.AfterID, page.Limit+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*scheduler.Node
	for rows.Next() {
		var nodeJSON []byte
		if err := rows.Scan(&nodeJSON); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}

		var node scheduler.Node
		if err := json.Unmarshal(nodeJSON, &node); err != nil {
			return nil, fmt.Errorf("unmarshaling node JSON: %w", err)
		}
		nodes = append(nodes, &node)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return scheduler.NewNodePageResponse(nodes, page.Limit), nil
}
