package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
)

// TryAssign atomically claims a queued task for candidate and reserves its
// resources in the candidate node. It leaves the task state unchanged; state
// and event propagation remain the responsibility of the event writer.
func (db *Postgres) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	if task == nil || task.GetId() == "" {
		return "", fmt.Errorf("try assign: task ID is required")
	}

	if timeout := db.conf.GetTimeout().GetDuration().AsDuration(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if candidate == nil {
		var state, owner string
		var active bool
		err := db.client.QueryRow(ctx, `SELECT state,COALESCE(claim_owner,''),claim_active FROM tasks WHERE id=$1`, task.Id).Scan(&state, &owner, &active)
		if err == pgx.ErrNoRows {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if state != tes.State_QUEUED.String() || owner == "" {
			return "", nil
		}
		if !active {
			return "", fmt.Errorf("task %q has an inactive allocation", task.Id)
		}
		return owner, nil
	}

	tx, err := db.client.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", fmt.Errorf("begin task assignment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var taskState, claimedOwner string
	var taskJSON []byte
	var claimActive bool
	err = tx.QueryRow(ctx, `
		SELECT state, COALESCE(claim_owner, ''), data,claim_active
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, task.Id).Scan(&taskState, &claimedOwner, &taskJSON, &claimActive)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lock task %q for assignment: %w", task.Id, err)
	}
	if taskState != tes.State_QUEUED.String() {
		return "", nil
	}
	var storedTask tes.Task
	if err := json.Unmarshal(taskJSON, &storedTask); err != nil {
		return "", fmt.Errorf("unmarshal task %q for assignment: %w", task.Id, err)
	}
	storedTask.Id = task.Id
	storedTask.State = tes.State_QUEUED

	if claimedOwner != "" {
		if !claimActive {
			return "", fmt.Errorf("task %q has an inactive allocation", task.Id)
		}
		return claimedOwner, nil
	}

	if candidate.GetId() == "" {
		return "", nil
	}

	var nodeJSON, usageJSON []byte
	var nodeVersion int64
	err = tx.QueryRow(ctx, `
		SELECT data, version,reserved
		FROM nodes
		WHERE id = $1
		FOR UPDATE
	`, candidate.Id).Scan(&nodeJSON, &nodeVersion, &usageJSON)
	if err == pgx.ErrNoRows {
		return "", fmt.Errorf("try assign task %q: candidate node %q not found", task.Id, candidate.Id)
	}
	if err != nil {
		return "", fmt.Errorf("lock candidate node %q for task %q: %w", candidate.Id, task.Id, err)
	}

	var node scheduler.Node
	if err := json.Unmarshal(nodeJSON, &node); err != nil {
		return "", fmt.Errorf("unmarshal candidate node %q: %w", candidate.Id, err)
	}
	if node.Id != candidate.Id {
		return "", fmt.Errorf("candidate node row %q contains node ID %q", candidate.Id, node.Id)
	}
	if node.Version != nodeVersion {
		return "", fmt.Errorf("candidate node %q has inconsistent stored version %d (row version %d)", candidate.Id, node.Version, nodeVersion)
	}
	if slices.Contains(node.TaskIds, task.Id) {
		return "", fmt.Errorf("node %q has a reservation for task %q without its authoritative claim", node.Id, task.Id)
	}
	var used scheduler.ResourceUsage
	if err := json.Unmarshal(usageJSON, &used); err != nil {
		return "", err
	}
	node.Available = used.Available(node.Resources)
	if !scheduler.Match(&node, &storedTask, scheduler.DefaultPredicates) {
		return "", fmt.Errorf("candidate node %q is no longer eligible", node.Id)
	}
	reserved, err := scheduler.TaskResourceUsage(&storedTask)
	if err != nil {
		return "", err
	}
	used, err = used.Add(reserved)
	if err != nil {
		return "", err
	}
	reservationJSON, err := json.Marshal(reserved)
	if err != nil {
		return "", err
	}
	node.TaskIds = append(node.TaskIds, task.Id)
	node.Available = used.Available(node.Resources)

	claimTag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET claim_owner = $1,claim_active=true,claim_resources=$4::jsonb
		WHERE id = $2
		  AND state = $3
		  AND COALESCE(claim_owner, '') = ''
	`, node.Id, task.Id, tes.State_QUEUED.String(), reservationJSON)
	if err != nil {
		return "", fmt.Errorf("claim task %q for node %q: %w", task.Id, node.Id, err)
	}
	if claimTag.RowsAffected() != 1 {
		return "", fmt.Errorf("task %q changed while reserving it on node %q", task.Id, node.Id)
	}

	node.Version = nodeVersion + 1
	if err := saveReservedNode(ctx, tx, &node, used); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit assignment of task %q to node %q: %w", task.Id, node.Id, err)
	}
	return node.Id, nil
}
