package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/jackc/pgx/v5"
)

func (db *Postgres) activeTaskIDs(ctx context.Context, nodeIDs []string) (map[string][]string, error) {
	ids := make(map[string][]string, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return ids, nil
	}
	rows, err := db.client.Query(ctx,
		`SELECT id,claim_owner FROM tasks WHERE claim_active AND claim_owner=ANY($1::varchar[]) ORDER BY claim_owner,id`,
		nodeIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, owner string
		if err := rows.Scan(&id, &owner); err != nil {
			return nil, err
		}
		ids[owner] = append(ids[owner], id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for owner := range ids {
		sort.Strings(ids[owner])
	}
	return ids, nil
}

func hydrateNodeProjection(node *scheduler.Node, version int64, usageJSON []byte, taskIDs []string) error {
	var usage scheduler.ResourceUsage
	if len(usageJSON) != 0 {
		if err := json.Unmarshal(usageJSON, &usage); err != nil {
			return fmt.Errorf("unmarshal node reservation: %w", err)
		}
	}
	node.Version = version
	node.Available = usage.Available(node.Resources)
	node.TaskIds = taskIDs
	return nil
}

func activeTaskIDsForNode(ctx context.Context, tx pgx.Tx, nodeID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM tasks WHERE claim_owner=$1 AND claim_active ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (db *Postgres) migrateNodeTaskIDProjection(ctx context.Context) error {
	const migration = "node-taskids-projection-v1"
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() //nolint:errcheck // Rolls back early exits and is a no-op after a successful commit.
	if _, err := tx.Exec(ctx, `LOCK TABLE scheduler_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_migrations WHERE name=$1)`, migration).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET data=data-'task_ids' WHERE data ? 'task_ids'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scheduler_migrations (name) VALUES ($1)`, migration); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
