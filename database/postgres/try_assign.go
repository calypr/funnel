package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

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

	tx, err := db.client.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", fmt.Errorf("begin task assignment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var taskState, claimedOwner string
	var taskJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT state, COALESCE(claim_owner, ''), data
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, task.Id).Scan(&taskState, &claimedOwner, &taskJSON)
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

	// Preserve and repair a legacy node assignment rather than assigning the
	// task to a second node. Lock matching node rows so their ownership cannot
	// change while the claim is written.
	assignmentRows, err := tx.Query(ctx, `
		SELECT id, data, version
		FROM nodes
		WHERE COALESCE(data->'task_ids', '[]'::jsonb) @> jsonb_build_array($1::text)
		ORDER BY id
		FOR UPDATE
	`, task.Id)
	if err != nil {
		return "", fmt.Errorf("find existing node assignment for task %q: %w", task.Id, err)
	}
	type assignedNode struct {
		id      string
		data    []byte
		version int64
	}
	var assigned []*assignedNode
	for assignmentRows.Next() {
		var row assignedNode
		if err := assignmentRows.Scan(&row.id, &row.data, &row.version); err != nil {
			assignmentRows.Close()
			return "", fmt.Errorf("read existing node assignment for task %q: %w", task.Id, err)
		}
		assigned = append(assigned, &row)
	}
	rowsErr := assignmentRows.Err()
	assignmentRows.Close()
	if rowsErr != nil {
		return "", fmt.Errorf("iterate existing node assignments for task %q: %w", task.Id, rowsErr)
	}
	if len(assigned) > 1 {
		return "", fmt.Errorf("task %q appears on multiple nodes; refusing ambiguous assignment", task.Id)
	}
	claimedNodeExists := func() (bool, error) {
		var nodeID string
		err := tx.QueryRow(ctx, `SELECT id FROM nodes WHERE id = $1 FOR UPDATE`, claimedOwner).Scan(&nodeID)
		if err == pgx.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("check claimed node %q for task %q: %w", claimedOwner, task.Id, err)
		}
		return true, nil
	}
	if len(assigned) == 1 {
		var assignedNodeData scheduler.Node
		if err := json.Unmarshal(assigned[0].data, &assignedNodeData); err != nil {
			return "", fmt.Errorf("unmarshal existing node assignment %q: %w", assigned[0].id, err)
		}
		if assignedNodeData.Version != assigned[0].version {
			return "", fmt.Errorf("existing node assignment %q has inconsistent stored version %d (row version %d)", assigned[0].id, assignedNodeData.Version, assigned[0].version)
		}
		if claimedOwner != "" {
			if assigned[0].id != claimedOwner {
				exists, err := claimedNodeExists()
				if err != nil {
					return "", err
				}
				if !exists {
					return claimedOwner, nil
				}
				return "", fmt.Errorf("task %q is claimed by node %q but appears on node %q", task.Id, claimedOwner, assigned[0].id)
			}
			return claimedOwner, nil
		}
		if candidate == nil {
			return assigned[0].id, nil
		}

		claimTag, err := tx.Exec(ctx, `
			UPDATE tasks
			SET claim_owner = $1
			WHERE id = $2 AND state = $3 AND COALESCE(claim_owner, '') = ''
		`, assigned[0].id, task.Id, tes.State_QUEUED.String())
		if err != nil {
			return "", fmt.Errorf("claim existing node assignment %q for task %q: %w", assigned[0].id, task.Id, err)
		}
		if claimTag.RowsAffected() != 1 {
			return "", fmt.Errorf("task %q changed while repairing node assignment %q", task.Id, assigned[0].id)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit existing assignment of task %q to node %q: %w", task.Id, assigned[0].id, err)
		}
		return assigned[0].id, nil
	}
	if claimedOwner != "" {
		exists, err := claimedNodeExists()
		if err != nil {
			return "", err
		}
		if !exists {
			// Return the durable owner so the scheduler can apply its missing-node
			// failure path. No new assignment can be made for an immutable claim.
			return claimedOwner, nil
		}
		return "", fmt.Errorf("task %q is claimed by node %q but has no node reservation", task.Id, claimedOwner)
	}
	if candidate == nil || candidate.GetId() == "" {
		return "", nil
	}

	var nodeJSON []byte
	var nodeVersion int64
	err = tx.QueryRow(ctx, `
		SELECT data, version
		FROM nodes
		WHERE id = $1
		FOR UPDATE
	`, candidate.Id).Scan(&nodeJSON, &nodeVersion)
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
	if candidate.Version != nodeVersion {
		return "", fmt.Errorf("candidate node %q version outdated: have %d, current %d", candidate.Id, candidate.Version, nodeVersion)
	}

	// A legacy assignment may already be present without a claim. Repair only
	// the missing claim; do not append or subtract the task a second time.
	if !slices.Contains(node.TaskIds, task.Id) {
		if !scheduler.Match(&node, &storedTask, scheduler.DefaultPredicates) {
			return "", fmt.Errorf("candidate node %q is no longer eligible for task %q", node.Id, task.Id)
		}

		node.TaskIds = append(node.TaskIds, task.Id)
		node.Available = scheduler.SubtractResources(&storedTask, node.Available)
	}

	claimTag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET claim_owner = $1
		WHERE id = $2
		  AND state = $3
		  AND COALESCE(claim_owner, '') = ''
	`, node.Id, task.Id, tes.State_QUEUED.String())
	if err != nil {
		return "", fmt.Errorf("claim task %q for node %q: %w", task.Id, node.Id, err)
	}
	if claimTag.RowsAffected() != 1 {
		return "", fmt.Errorf("task %q changed while reserving it on node %q", task.Id, node.Id)
	}

	node.Version = nodeVersion + 1
	node.LastPing = time.Now().UnixNano()
	updatedNodeJSON, err := json.Marshal(&node)
	if err != nil {
		return "", fmt.Errorf("marshal candidate node %q: %w", node.Id, err)
	}
	nodeTag, err := tx.Exec(ctx, `
		UPDATE nodes
		SET data = $1::jsonb, version = $2, last_heartbeat = $3
		WHERE id = $4 AND version = $5
	`, updatedNodeJSON, node.Version, time.Unix(0, node.LastPing), node.Id, nodeVersion)
	if err != nil {
		return "", fmt.Errorf("reserve task %q on node %q: %w", task.Id, node.Id, err)
	}
	if nodeTag.RowsAffected() != 1 {
		return "", fmt.Errorf("candidate node %q version changed while reserving task %q", node.Id, task.Id)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit assignment of task %q to node %q: %w", task.Id, node.Id, err)
	}
	return node.Id, nil
}
