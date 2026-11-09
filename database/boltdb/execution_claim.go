package boltdb

import (
	"context"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

// ClaimExecution atomically admits one worker for a queued or initializing task.
func (db *BoltDB) ClaimExecution(ctx context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var ownerMetadata map[string]string
	if owner != nil {
		var err error
		ownerMetadata, err = events.ExecutionOwnerMetadata(owner)
		if err != nil {
			return false, err
		}
	}
	claimed := false
	err := db.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := []byte(taskID)
		if tx.Bucket(TaskBucket).Get(key) == nil {
			return tes.ErrNotFound
		}
		if err := checkOwner(tx, taskID, ctx); err != nil {
			return err
		}
		if tx.Bucket(ExecutionClaims).Get(key) != nil {
			return nil
		}
		state := getTaskState(tx, taskID)
		if state != tes.State_QUEUED && state != tes.State_INITIALIZING {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if ownerMetadata != nil {
			if err := updateTaskLogs(tx, taskID, &tes.TaskLog{Metadata: ownerMetadata}); err != nil {
				return err
			}
		}
		if err := tx.Bucket(TasksQueued).Delete(key); err != nil {
			return err
		}
		if err := tx.Bucket(TaskState).Put(key, []byte(tes.State_INITIALIZING.String())); err != nil {
			return err
		}
		if err := tx.Bucket(ExecutionClaims).Put(key, []byte{1}); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}
