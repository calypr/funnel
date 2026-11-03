package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

// WriteEvent creates an event for the server to handle.
func (db *Postgres) WriteEvent(ctx context.Context, req *events.Event) error {
	logger := logger.NewLogger("postgres", logger.DefaultConfig())

	ctx, cancel := context.WithTimeout(ctx, db.conf.GetTimeout().GetDuration().AsDuration())
	defer cancel()

	selector := req.Id

	logger.Debug("Postgres", "WriteEvent request:", req)
	switch req.Type {

	// Task Created
	case events.Type_TASK_CREATED:
		task := req.GetTask()
		if task.Logs == nil || len(task.Logs) == 0 {
			executorLogs := make([]*tes.ExecutorLog, len(task.Executors))
			for i := range executorLogs {
				executorLogs[i] = &tes.ExecutorLog{
					StartTime: "",
					EndTime:   "",
					Stdout:    "",
					Stderr:    "",
				}
			}

			task.Logs = []*tes.TaskLog{
				{
					StartTime:  "",
					EndTime:    "",
					Logs:       executorLogs,
					Metadata:   map[string]string{},
					SystemLogs: []string{},
					Outputs:    []*tes.OutputFileLog{},
				},
			}
		}

		return db.insertTask(ctx, task, server.GetUsername(ctx))

	// Task State change
	case events.Type_TASK_STATE:
		return db.transitionReservedTask(ctx, req.Id, req.GetState())

	// Task + Executor Events
	case events.Type_TASK_START_TIME, events.Type_TASK_END_TIME, events.Type_TASK_OUTPUTS, events.Type_TASK_METADATA,
		events.Type_EXECUTOR_START_TIME, events.Type_EXECUTOR_END_TIME, events.Type_EXECUTOR_EXIT_CODE,
		events.Type_EXECUTOR_STDOUT, events.Type_EXECUTOR_STDERR:

		var jsonPath string
		var jsonValue interface{}

		switch req.Type {

		// Task Start Time
		case events.Type_TASK_START_TIME:
			jsonPath = fmt.Sprintf("{logs,%v,start_time}", req.Attempt)
			jsonValue = req.GetStartTime()

		// Task End Time
		case events.Type_TASK_END_TIME:
			jsonPath = fmt.Sprintf("{logs,%v,end_time}", req.Attempt)
			jsonValue = req.GetEndTime()

		// Task Outputs
		case events.Type_TASK_OUTPUTS:
			jsonPath = fmt.Sprintf("{logs,%v,outputs}", req.Attempt)
			jsonValue = req.GetOutputs().Value

		// Task Metadata
		case events.Type_TASK_METADATA:
			metadata := req.GetMetadata().Value
			if _, reserved := metadata[events.ExecutionOwnerMetadataKey]; reserved {
				return fmt.Errorf("task metadata key %q is reserved", events.ExecutionOwnerMetadataKey)
			}
			for k, v := range metadata {
				path := []string{"logs", strconv.FormatUint(uint64(req.Attempt), 10), "metadata", k}
				updateSQL := `
                    UPDATE tasks 
                    SET data = jsonb_set(data, $1::text[], $2::jsonb, true)
                    WHERE id = $3
                `
				value, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if _, err := db.client.Exec(ctx, updateSQL, path, value, selector); err != nil {
					return fmt.Errorf("failed to update task metadata %q: %w", k, err)
				}
			}
			return nil

		// Executor Start Time
		case events.Type_EXECUTOR_START_TIME:
			jsonPath = fmt.Sprintf("{logs,%v,logs,%v,start_time}", req.Attempt, req.Index)
			jsonValue = req.GetStartTime()

		// Executor End Time
		case events.Type_EXECUTOR_END_TIME:
			jsonPath = fmt.Sprintf("{logs,%v,logs,%v,end_time}", req.Attempt, req.Index)
			jsonValue = req.GetEndTime()

		// Executor Exit Code
		case events.Type_EXECUTOR_EXIT_CODE:
			jsonPath = fmt.Sprintf("{logs,%v,logs,%v,exit_code}", req.Attempt, req.Index)
			jsonValue = req.GetExitCode()

		// Executor STDOUT
		case events.Type_EXECUTOR_STDOUT:
			jsonPath = fmt.Sprintf("{logs,%v,logs,%v,stdout}", req.Attempt, req.Index)
			jsonValue = req.GetStdout()

		// STDERR
		case events.Type_EXECUTOR_STDERR:
			jsonPath = fmt.Sprintf("{logs,%v,logs,%v,stderr}", req.Attempt, req.Index)
			jsonValue = req.GetStderr()
		}

		// Use jsonb_set to update a single field in the JSONB document.
		// The `true` parameter ensures that the path is created if it doesn't exist.
		updateSQL := `
			UPDATE tasks 
			SET data = jsonb_set(data, $1::text[], $2::jsonb, true)
			WHERE id = $3
		`
		jsonVal, err := json.Marshal(jsonValue)
		if err != nil {
			return fmt.Errorf("marshal task event value: %w", err)
		}
		_, err = db.client.Exec(ctx, updateSQL, jsonPath, jsonVal, selector)

		// Log error
		return err

	// System Log
	case events.Type_SYSTEM_LOG:
		jsonPath := fmt.Sprintf("{logs,%v,system_logs}", req.Attempt)
		logValue := req.SysLogString()

		selectSQL := `
            SELECT data #> '{logs,0,system_logs}' 
            FROM tasks
            WHERE id = $1
        `
		var currentLogsJSON []byte
		err := db.client.QueryRow(ctx, selectSQL, selector).Scan(&currentLogsJSON)
		if err != nil {
			return fmt.Errorf("failed to read current system logs for task %s: %w", selector, err)
		}

		var currentLogs []string
		if len(currentLogsJSON) > 0 {
			if err := json.Unmarshal(currentLogsJSON, &currentLogs); err != nil {
				return fmt.Errorf("failed to unmarshal current system logs for task %s: %w", selector, err)
			}
		}

		currentLogs = append(currentLogs, logValue)

		newLogsJSON, err := json.Marshal(currentLogs)
		if err != nil {
			return fmt.Errorf("marshal task system logs: %w", err)
		}

		updateSQL := `
            UPDATE tasks
            SET data = jsonb_set(data, $1::text[], $2::jsonb)
            WHERE id = $3
        `
		_, err = db.client.Exec(ctx, updateSQL, jsonPath, newLogsJSON, selector)
		return err
	}

	return nil
}

func (db *Postgres) insertTask(ctx context.Context, task *tes.Task, owner string) error {
	task.CreationTime = time.Now().Format(time.RFC3339Nano)
	task.State = tes.State_QUEUED
	for _, taskLog := range task.Logs {
		if taskLog != nil {
			delete(taskLog.Metadata, events.ExecutionOwnerMetadataKey)
		}
	}

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task to JSON: %w", err)
	}

	insertSQL := `
		INSERT INTO tasks (id, state, owner, creation_time, version, data) 
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`
	version := time.Now().UnixNano()

	_, err = db.client.Exec(ctx, insertSQL,
		task.Id,
		task.State.String(),
		owner,
		task.CreationTime,
		version,
		taskJSON,
	)

	return err
}
