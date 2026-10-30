package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/jackc/pgx/v5"
)

func (db *Postgres) migrateNodeHeartbeats(ctx context.Context) error {

	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() //nolint:errcheck // Rolls back early exits and is a no-op after a successful commit.
	if _, err := tx.Exec(ctx, `LOCK TABLE scheduler_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_migrations WHERE name='separate-node-heartbeats-v1')`).Scan(&done); err != nil {
		return err
	}
	if !done {
		if _, err := tx.Exec(ctx, `INSERT INTO node_heartbeats (id,last_ping) SELECT id, COALESCE((data->>'last_ping')::bigint,0) FROM nodes ON CONFLICT (id) DO NOTHING`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO scheduler_migrations (name) VALUES ('separate-node-heartbeats-v1')`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (db *Postgres) putReportedNode(ctx context.Context, report *scheduler.Node) error {
	if report.Id == "" {
		return fmt.Errorf("node ID is required")
	}
	if err := scheduler.ValidateNodeCapacity(report.Resources); err != nil {
		return err
	}
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() //nolint:errcheck // Rolls back early exits and is a no-op after a successful commit.
	var stamp int64
	// All status mutations acquire the heartbeat row before the node row.
	// Allocation transactions never acquire the heartbeat row.
	if err := tx.QueryRow(ctx, `INSERT INTO node_heartbeats (id,last_ping) VALUES ($1,$2)
  ON CONFLICT (id) DO UPDATE SET last_ping=GREATEST(node_heartbeats.last_ping+1,EXCLUDED.last_ping)
  RETURNING last_ping`, report.Id, time.Now().UnixNano()).Scan(&stamp); err != nil {
		return err
	}
	var data, reservedJSON []byte
	var version int64
	var nodeState string
	err = tx.QueryRow(ctx, `SELECT data,version,state,reserved FROM nodes WHERE id=$1`, report.Id).Scan(&data, &version, &nodeState, &reservedJSON)
	isNew := err == pgx.ErrNoRows
	if err != nil && !isNew {
		return err
	}
	if isNew {
		var allocated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE claim_owner=$1 AND claim_active)`, report.Id).Scan(&allocated); err != nil {
			return err
		}
		if allocated {
			return fmt.Errorf("node ID %q still owns active allocations; register with a new ID", report.Id)
		}
	}
	current := &scheduler.Node{}
	var used scheduler.ResourceUsage
	if !isNew {
		if err := json.Unmarshal(data, current); err != nil {
			return err
		}
		state, ok := scheduler.NodeState_value[nodeState]
		if !ok {
			return fmt.Errorf("node %q has invalid state %q", report.Id, nodeState)
		}
		current.State, current.Version = scheduler.NodeState(state), version
		if err := json.Unmarshal(reservedJSON, &used); err != nil {
			return err
		}
		if current.State == scheduler.NodeState_GONE && report.State != scheduler.NodeState_GONE {
			return fmt.Errorf("gone node %q cannot re-register with the same ID", report.Id)
		}
	}
	nodeChanged := isNew || scheduler.NodeReportChanged(report, current)
	if nodeChanged {
		if !isNew {
			if err := tx.QueryRow(ctx, `SELECT data,version,state,reserved FROM nodes WHERE id=$1 FOR UPDATE`, report.Id).Scan(&data, &version, &nodeState, &reservedJSON); err != nil {
				return err
			}
			if err := json.Unmarshal(data, current); err != nil {
				return err
			}
			state, ok := scheduler.NodeState_value[nodeState]
			if !ok {
				return fmt.Errorf("node %q has invalid state %q", report.Id, nodeState)
			}
			current.State, current.Version = scheduler.NodeState(state), version
			if err := json.Unmarshal(reservedJSON, &used); err != nil {
				return err
			}
		}
		if report.Version != version {
			return scheduler.ErrNodeChanged
		}
		current.Id = report.Id
		scheduler.ApplyNodeReport(current, report)
		current.Available = used.Available(current.Resources)
		current.Version = version + 1
		current.TaskIds = nil
		data, err = json.Marshal(current)
		if err != nil {
			return err
		}
		if isNew {
			_, err = tx.Exec(ctx, `INSERT INTO nodes (id,state,version,data) VALUES ($1,$2,$3,$4::jsonb)`, current.Id, current.State.String(), current.Version, data)
		} else {
			_, err = tx.Exec(ctx, `UPDATE nodes SET state=$2,version=$3,data=$4::jsonb WHERE id=$1`, current.Id, current.State.String(), current.Version, data)
		}
		if err != nil {
			return err
		}
	}
	current.Available = used.Available(current.Resources)
	if isNew {
		current.Version = 1
	} else if !nodeChanged {
		current.Version = version
	}
	current.TaskIds, err = activeTaskIDsForNode(ctx, tx, report.Id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	report.Version, report.LastPing = current.Version, stamp
	report.TaskIds, report.Available = current.TaskIds, current.Available
	return nil
}

func (db *Postgres) SetNodeState(ctx context.Context, observed *scheduler.Node) (*scheduler.Node, error) {
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() //nolint:errcheck // Rolls back early exits and is a no-op after a successful commit.
	var stamp int64
	err = tx.QueryRow(ctx, `SELECT last_ping FROM node_heartbeats WHERE id=$1 FOR UPDATE`, observed.Id).Scan(&stamp)
	if err == pgx.ErrNoRows {
		return nil, scheduler.ErrNodeChanged
	}
	if err != nil {
		return nil, err
	}
	var data, reservedJSON []byte
	var version int64
	var state string
	err = tx.QueryRow(ctx, `SELECT data,version,state,reserved FROM nodes WHERE id=$1 FOR UPDATE`, observed.Id).Scan(&data, &version, &state, &reservedJSON)
	if err == pgx.ErrNoRows {
		return nil, scheduler.ErrNodeChanged
	}
	if err != nil {
		return nil, err
	}
	if version != observed.Version || stamp != observed.LastPing {
		return nil, scheduler.ErrNodeChanged
	}
	var node scheduler.Node
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	stateValue, ok := scheduler.NodeState_value[state]
	if !ok {
		return nil, fmt.Errorf("node %q has invalid state %q", observed.Id, state)
	}
	node.State, node.Version = scheduler.NodeState(stateValue), version
	var used scheduler.ResourceUsage
	if err := json.Unmarshal(reservedJSON, &used); err != nil {
		return nil, err
	}
	node.Available = used.Available(node.Resources)
	node.TaskIds = nil
	if node.State != observed.State {
		node.State = observed.State
		node.Version = version + 1
		if _, err := tx.Exec(ctx, `UPDATE nodes SET state=$2,version=$3 WHERE id=$1 AND version=$4`, node.Id, node.State.String(), node.Version, version); err != nil {
			return nil, err
		}
	}
	node.TaskIds, err = activeTaskIDsForNode(ctx, tx, node.Id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	node.LastPing = stamp
	return &node, nil
}
