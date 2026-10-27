package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
)

func (db *Postgres) migrateResourceReservations(ctx context.Context) error {
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE scheduler_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_migrations WHERE name='resource-reservations-v1')`).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE tasks,nodes IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,claim_owner,data FROM tasks WHERE COALESCE(claim_owner,'') <> '' AND state NOT IN ('COMPLETE','CANCELED','EXECUTOR_ERROR','SYSTEM_ERROR') ORDER BY id`)
	if err != nil {
		return err
	}
	type allocation struct {
		id, owner string
		usage     scheduler.ResourceUsage
	}
	var allocations []allocation
	totals := make(map[string]scheduler.ResourceUsage)
	ids := make(map[string][]string)
	for rows.Next() {
		var a allocation
		var data []byte
		if err := rows.Scan(&a.id, &a.owner, &data); err != nil {
			rows.Close()
			return err
		}
		var task tes.Task
		if err := json.Unmarshal(data, &task); err != nil {
			rows.Close()
			return err
		}
		a.usage, err = scheduler.TaskResourceUsage(&task)
		if err != nil {
			rows.Close()
			return err
		}
		totals[a.owner], err = totals[a.owner].Add(a.usage)
		if err != nil {
			rows.Close()
			return err
		}
		ids[a.owner] = append(ids[a.owner], a.id)
		allocations = append(allocations, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range allocations {
		data, err := json.Marshal(a.usage)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET claim_resources=$2::jsonb,claim_active=true WHERE id=$1`, a.id, data); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `SELECT data,version FROM nodes`)
	if err != nil {
		return err
	}
	var nodes []*scheduler.Node
	for rows.Next() {
		var data []byte
		var version int64
		if err := rows.Scan(&data, &version); err != nil {
			rows.Close()
			return err
		}
		var node scheduler.Node
		if err := json.Unmarshal(data, &node); err != nil {
			rows.Close()
			return err
		}
		node.Version = version + 1
		node.TaskIds = ids[node.Id]
		node.Available = totals[node.Id].Available(node.Resources)
		nodes = append(nodes, &node)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err := saveReservedNode(ctx, tx, node, totals[node.Id]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scheduler_migrations (name) VALUES ('resource-reservations-v1')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func saveReservedNode(ctx context.Context, tx pgx.Tx, node *scheduler.Node, used scheduler.ResourceUsage) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	usage, err := json.Marshal(used)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE nodes SET data=$2::jsonb,version=$3,reserved=$4::jsonb WHERE id=$1`, node.Id, data, node.Version, usage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("reservation node %q disappeared", node.Id)
	}
	return nil
}

// The task row is locked by the caller before the node row. Only an active
// allocation releases capacity; its snapshot is independent of later task edits.
func releaseTaskReservation(ctx context.Context, tx pgx.Tx, taskID, owner string, reservation []byte) error {
	var held scheduler.ResourceUsage
	if len(reservation) == 0 {
		return fmt.Errorf("active task %q has no reservation snapshot", taskID)
	}
	if err := json.Unmarshal(reservation, &held); err != nil {
		return err
	}
	var data, usage []byte
	var version int64
	err := tx.QueryRow(ctx, `SELECT data,version,reserved FROM nodes WHERE id=$1 FOR UPDATE`, owner).Scan(&data, &version, &usage)
	if err == pgx.ErrNoRows {
		return nil
	} // Missing-node recovery still retires the task's allocation.
	if err != nil {
		return err
	}
	var node scheduler.Node
	var used scheduler.ResourceUsage
	if err := json.Unmarshal(data, &node); err != nil {
		return err
	}
	if err := json.Unmarshal(usage, &used); err != nil {
		return err
	}
	used, err = used.Release(held)
	if err != nil {
		return err
	}
	node.TaskIds = slices.DeleteFunc(node.TaskIds, func(id string) bool { return id == taskID })
	node.Available = used.Available(node.Resources)
	node.Version = version + 1
	return saveReservedNode(ctx, tx, &node, used)
}

func (db *Postgres) transitionReservedTask(ctx context.Context, id string, target tes.State) error {
	tx, err := db.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var state, owner, claim string
	var version int64
	var active bool
	var held []byte
	err = tx.QueryRow(ctx, `SELECT state,COALESCE(owner,''),version,COALESCE(claim_owner,''),claim_active,claim_resources FROM tasks WHERE id=$1 FOR UPDATE`, id).Scan(&state, &owner, &version, &claim, &active, &held)
	if err == pgx.ErrNoRows {
		return tes.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !server.GetUser(ctx).IsAccessible(owner) {
		return tes.ErrNotPermitted
	}
	value, ok := tes.State_value[state]
	if !ok {
		return fmt.Errorf("task %q has invalid state %q", id, state)
	}
	current := tes.State(value)
	if err := tes.ValidateTransition(current, target); err != nil {
		return err
	}
	if claim != "" && tes.TerminalState(current) && !tes.TerminalState(target) {
		return fmt.Errorf("task %q has a finished allocation; submit a new task to retry", id)
	}
	if tes.TerminalState(target) && active {
		if claim == "" {
			return fmt.Errorf("active task %q has no allocation owner", id)
		}
		if err := releaseTaskReservation(ctx, tx, id, claim, held); err != nil {
			return err
		}
		active = false
	}
	stateJSON, err := json.Marshal(int32(target))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET state=$2,version=$3,data=jsonb_set(data,'{state}',$4::jsonb),claim_active=$5 WHERE id=$1`, id, target.String(), version+1, stateJSON, active); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
