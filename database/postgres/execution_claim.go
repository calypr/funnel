package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (db *Postgres) migrateExecutionClaims(ctx context.Context) error {
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE scheduler_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_migrations WHERE name = 'execution-claims-v1')`).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE tasks IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET execution_claimed=true WHERE state IN ('RUNNING','COMPLETE','CANCELED','EXECUTOR_ERROR','SYSTEM_ERROR')`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scheduler_migrations (name) VALUES ('execution-claims-v1')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClaimExecution atomically admits one worker for a queued or initializing task.
func (db *Postgres) ClaimExecution(ctx context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	var ownerMetadataJSON []byte
	if owner != nil {
		ownerMetadata, err := events.ExecutionOwnerMetadata(owner)
		if err != nil {
			return false, err
		}
		ownerMetadataJSON, err = json.Marshal(ownerMetadata)
		if err != nil {
			return false, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()

	tx, err := db.client.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var state, username string
	var version int64
	var alreadyClaimed bool
	err = tx.QueryRow(ctx, `SELECT state,COALESCE(owner,''),version,execution_claimed FROM tasks WHERE id=$1 FOR UPDATE`, taskID).
		Scan(&state, &username, &version, &alreadyClaimed)
	if err == pgx.ErrNoRows {
		return false, tes.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !server.GetUser(ctx).IsAccessible(username) {
		return false, tes.ErrNotPermitted
	}
	if alreadyClaimed || (state != tes.State_QUEUED.String() && state != tes.State_INITIALIZING.String()) {
		return false, nil
	}
	stateJSON, err := json.Marshal(int32(tes.State_INITIALIZING))
	if err != nil {
		return false, err
	}
	var tag pgconn.CommandTag
	if ownerMetadataJSON == nil {
		tag, err = tx.Exec(ctx, `UPDATE tasks SET state=$2,execution_claimed=true,version=$3,data=jsonb_set(data,'{state}',$4::jsonb) WHERE id=$1`,
			taskID, tes.State_INITIALIZING.String(), version+1, stateJSON)
	} else {
		tag, err = tx.Exec(ctx, `UPDATE tasks
			SET state=$2,execution_claimed=true,version=$3,
			    data=jsonb_set(
			        jsonb_set(
			            jsonb_set(
			                jsonb_set(data,'{state}',$4::jsonb),
			                '{logs}',
			                CASE WHEN jsonb_typeof(data->'logs')='array' THEN data->'logs' ELSE '[{}]'::jsonb END,
			                true),
			            '{logs,0}',
			            CASE WHEN jsonb_typeof(data #> '{logs,0}')='object' THEN data #> '{logs,0}' ELSE '{}'::jsonb END,
			            true),
			        '{logs,0,metadata}',
			        CASE WHEN jsonb_typeof(data #> '{logs,0,metadata}')='object'
			             THEN data #> '{logs,0,metadata}' ELSE '{}'::jsonb END || $5::jsonb,
			        true)
			WHERE id=$1`,
			taskID, tes.State_INITIALIZING.String(), version+1, stateJSON, ownerMetadataJSON)
	}
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, fmt.Errorf("task %q disappeared while claiming execution", taskID)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
