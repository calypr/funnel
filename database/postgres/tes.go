package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

// trimUnusedExecutorLogs removes trailing executor log entries that correspond
// to executors that never ran. On task creation the Postgres backend
// pre-allocates one empty ExecutorLog per executor (see WriteEvent /
// Type_TASK_CREATED) because jsonb_set cannot grow a JSON array in place. When a
// task stops early (e.g. an executor fails with ignore_error=false), the slots
// for the executors that never ran remain as empty placeholders. An executor
// that never started has no StartTime, so trim trailing logs with an empty
// StartTime to match the lazily-grown shape produced by the other backends.
func trimUnusedExecutorLogs(task *tes.Task) {
	for _, tl := range task.GetLogs() {
		logs := tl.Logs
		for len(logs) > 0 && logs[len(logs)-1].GetStartTime() == "" {
			logs = logs[:len(logs)-1]
		}
		tl.Logs = logs
	}
}

// The PostgreSQL struct for minimal projection (only fields outside the JSONB blob)
type TaskCore struct {
	ID       string `db:"id"`
	Owner    string `db:"owner"`
	StateStr string `db:"state"`
	DataJSON []byte `db:"data"`
}

func taskView(view string) (func(*tes.Task) *tes.Task, error) {
	switch view {
	case "", tes.View_MINIMAL.String():
		return (*tes.Task).GetMinimalView, nil
	case tes.View_BASIC.String():
		return (*tes.Task).GetBasicView, nil
	case tes.View_FULL.String():
		return func(task *tes.Task) *tes.Task { return task }, nil
	default:
		return nil, fmt.Errorf("unknown task view %q: must be one of MINIMAL, BASIC, FULL", view)
	}
}

// GetTask gets a task.
func (db *Postgres) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	project, err := taskView(req.View)
	if err != nil {
		return nil, err
	}

	ctx, cancel := db.context()
	defer cancel()

	var selectFields string
	// For PostgreSQL with JSONB, it's simplest to fetch the full JSONB, then project in Go,
	// or fetch the full JSONB and explicitly select indexed fields.

	// We always fetch core fields and the full JSONB.
	selectFields = "id, owner, state, data"

	selectSQL := fmt.Sprintf("SELECT %s FROM tasks WHERE id = $1", selectFields)

	var core TaskCore
	var stateStr string

	err = db.client.QueryRow(ctx, selectSQL, req.Id).Scan(&core.ID, &core.Owner, &stateStr, &core.DataJSON)

	if err != nil {
		return nil, tes.ErrNotFound
	}

	// Authorization Check
	if !server.GetUser(ctx).IsAccessible(core.Owner) {
		return nil, tes.ErrNotPermitted
	}

	var task tes.Task
	if err := json.Unmarshal(core.DataJSON, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task JSON: %w", err)
	}

	task.Id = core.ID
	task.State = tes.State(tes.State_value[stateStr])
	// task.Owner = core.Owner

	trimUnusedExecutorLogs(&task)

	return project(&task), nil
}

// ListTasks returns a list of tasks.
func (db *Postgres) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	project, err := taskView(req.View)
	if err != nil {
		return nil, err
	}

	ctx, cancel := db.context()
	defer cancel()

	pageSize := tes.GetPageSize(req.GetPageSize())

	var args []interface{}
	var whereClauses []string
	paramCount := 1

	// Name prefix filter
	if req.NamePrefix != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("data ->> 'name' LIKE $%d", paramCount))
		args = append(args, req.NamePrefix+"%") // PostgreSQL LIKE operator needs % for prefix match
		paramCount++
	}

	// State filter. FIXME: The logic fails to fetch the records for tasks in the UNKNOWN state, but we currently have no tasks in that state, so it isn't a problem.
	// We should review this logic when we add support for UNKNOWN state tasks.
	if req.State != tes.State_UNKNOWN {
		whereClauses = append(whereClauses, fmt.Sprintf("state = $%d", paramCount))
		args = append(args, req.State)
		paramCount++
	}

	// Authorization filter
	if userInfo := server.GetUser(ctx); !userInfo.CanSeeAllTasks() {
		whereClauses = append(whereClauses, fmt.Sprintf("owner = $%d", paramCount))
		args = append(args, userInfo.Username)
		paramCount++
	}

	// Tags Filter
	for k, v := range req.GetTags() {
		if v == "" {
			// Check if tag key exists: `data -> 'tags' ? 'key'`
			whereClauses = append(whereClauses, fmt.Sprintf("data -> 'tags' ? $%d", paramCount))
			args = append(args, k)
		} else {
			// Check if tag value equals: `data -> 'tags' ->> 'key' = 'value'`
			whereClauses = append(whereClauses, fmt.Sprintf("data -> 'tags' ->> $%d = $%d", paramCount, paramCount+1))
			args = append(args, k, v)
			paramCount++
		}
		paramCount++
	}

	// Page Token
	if req.PageToken != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("id < $%d", paramCount))
		args = append(args, req.PageToken)
		paramCount++
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	orderByClause := "ORDER BY creation_time DESC, id DESC"
	limitClause := fmt.Sprintf("LIMIT $%d", paramCount)
	args = append(args, pageSize)

	selectSQL := fmt.Sprintf("SELECT data FROM tasks %s %s %s", whereClause, orderByClause, limitClause)

	rows, err := db.client.Query(ctx, selectSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*tes.Task
	for rows.Next() {
		var taskJSON []byte
		if err := rows.Scan(&taskJSON); err != nil {
			fmt.Println("Error scanning task row for ListTasks:", err)
			continue
		}

		var task tes.Task
		if err := json.Unmarshal(taskJSON, &task); err != nil {
			fmt.Println("Error unmarshaling task JSON for ListTasks:", err)
			continue
		}

		trimUnusedExecutorLogs(&task)

		tasks = append(tasks, project(&task))
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	out := tes.ListTasksResponse{
		Tasks: tasks,
	}
	// Determine NextPageToken
	if len(tasks) == pageSize {
		out.NextPageToken = tasks[len(tasks)-1].Id
	}

	return &out, nil
}
