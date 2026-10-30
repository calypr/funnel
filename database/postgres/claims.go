package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Migration is serialized across server startups and commits with its marker.
// Stop older schedulers before upgrading; normal writes never import TaskIds.
func (db *Postgres) migrateTaskClaims(ctx context.Context) error {

	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE scheduler_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_migrations WHERE name = 'authoritative-task-claims-v1')`).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE tasks, nodes IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	checks := []struct{ query, message string }{
		{`SELECT id FROM nodes WHERE data->>'id' IS DISTINCT FROM id LIMIT 1`, "stored node ID does not match its key"},
		{`SELECT t.id FROM nodes n
    CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(n.data->'task_ids', '[]'::jsonb)) a(task_id)
    JOIN tasks t ON t.id = a.task_id
    WHERE t.state NOT IN ('COMPLETE', 'CANCELED', 'EXECUTOR_ERROR', 'SYSTEM_ERROR')
    GROUP BY t.id HAVING COUNT(DISTINCT n.id) > 1 LIMIT 1`, "task is assigned to multiple nodes"},
		{`SELECT t.id FROM nodes n
    CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(n.data->'task_ids', '[]'::jsonb)) a(task_id)
    JOIN tasks t ON t.id = a.task_id
    WHERE t.state NOT IN ('COMPLETE', 'CANCELED', 'EXECUTOR_ERROR', 'SYSTEM_ERROR')
      AND COALESCE(t.claim_owner, '') <> '' AND t.claim_owner <> n.id LIMIT 1`, "task claim conflicts with its legacy reservation"},
		{`SELECT t.id FROM tasks t JOIN nodes n ON n.id = t.claim_owner
    WHERE t.state NOT IN ('COMPLETE', 'CANCELED', 'EXECUTOR_ERROR', 'SYSTEM_ERROR')
      AND NOT (COALESCE(n.data->'task_ids', '[]'::jsonb) @> jsonb_build_array(t.id::text)) LIMIT 1`, "task claim has no reservation on its existing owner"},
	}
	for _, check := range checks {
		var id string
		err := tx.QueryRow(ctx, check.query).Scan(&id)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("validate legacy task claims: %w", err)
		}
		return fmt.Errorf("cannot migrate task claims: %s (%q); resolve before upgrading", check.message, id)
	}
	if _, err := tx.Exec(ctx, `
  WITH legacy AS (
   SELECT DISTINCT t.id AS task_id, n.id AS node_id FROM nodes n
   CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(n.data->'task_ids', '[]'::jsonb)) a(task_id)
   JOIN tasks t ON t.id = a.task_id
   WHERE t.state NOT IN ('COMPLETE', 'CANCELED', 'EXECUTOR_ERROR', 'SYSTEM_ERROR')
  )
  UPDATE tasks t SET claim_owner = legacy.node_id FROM legacy
  WHERE t.id = legacy.task_id AND COALESCE(t.claim_owner, '') = ''`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scheduler_migrations (name) VALUES ('authoritative-task-claims-v1')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
