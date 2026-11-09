package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
)

func TestPostgresEventAndPrefixRegressions(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate Postgres test container: %v", err)
		}
	})

	ctx := postgresQAOwnerContext(t)
	t.Run("missing task system log is rejected", func(t *testing.T) {
		err := db.WriteEvent(ctx, events.NewSystemLog("missing-task", 0, 0, "info", "missing", nil))
		if err == nil {
			t.Fatal("system log for nonexistent task was acknowledged")
		}
	})

	t.Run("literal name prefixes survive pagination and owner filtering", func(t *testing.T) {
		fixtures := []*tes.Task{
			newTestTask("prefix-underscore", "a_b", testUser),
			newTestTask("prefix-double-underscore", "a__c", testUser),
			newTestTask("prefix-wildcard", "axb", testUser),
			newTestTask("prefix-percent", "a%literal", testUser),
			newTestTask("prefix-backslash", "a\\literal", testUser),
			newTestTask("prefix-double-backslash", "a\\\\literal", testUser),
			newTestTask("prefix-other-owner", "a_owner", "other"),
		}
		for _, task := range fixtures {
			writeCtx := ctx
			if task.Id == "prefix-other-owner" {
				writeCtx = context.WithValue(ctx, server.UserInfoKey, &server.UserInfo{Username: "other"})
			}
			if err := db.WriteEvent(writeCtx, events.NewTaskCreated(task)); err != nil {
				t.Fatalf("create task %q: %v", task.Id, err)
			}
		}

		cases := []struct {
			prefix string
			want   map[string]struct{}
		}{
			{prefix: "a_", want: ids("prefix-underscore", "prefix-double-underscore")},
			{prefix: "a%", want: ids("prefix-percent")},
			{prefix: "a\\", want: ids("prefix-backslash", "prefix-double-backslash")},
		}
		for _, tc := range cases {
			t.Run("prefix="+tc.prefix, func(t *testing.T) {
				got := make(map[string]struct{})
				token := ""
				for {
					page, err := db.ListTasks(ctx, &tes.ListTasksRequest{
						NamePrefix: tc.prefix,
						PageSize:   1,
						PageToken:  token,
						View:       tes.View_BASIC.String(),
					})
					if err != nil {
						t.Fatalf("list prefix %q: %v", tc.prefix, err)
					}
					for _, task := range page.Tasks {
						got[task.Id] = struct{}{}
					}
					if page.NextPageToken == "" {
						break
					}
					token = page.NextPageToken
				}
				if len(got) != len(tc.want) {
					t.Fatalf("prefix %q returned ids %v, want %v", tc.prefix, got, tc.want)
				}
				for id := range tc.want {
					if _, ok := got[id]; !ok {
						t.Errorf("prefix %q omitted %q from %v", tc.prefix, id, got)
					}
				}
			})
		}
	})

	t.Run("metadata writes create the omitted object and preserve sibling keys", func(t *testing.T) {
		emptyTask := newTestTask("metadata-empty", "metadata-empty", testUser)
		if err := db.WriteEvent(ctx, events.NewTaskCreated(emptyTask)); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteEvent(ctx, events.NewMetadata(emptyTask.Id, 0, nil)); err != nil {
			t.Fatal(err)
		}
		if claimed, err := db.ClaimExecution(ctx, emptyTask.Id, nil); err != nil || !claimed {
			t.Fatalf("claim empty metadata task=%v, %v", claimed, err)
		}
		if err := db.WriteEvent(ctx, events.NewMetadata(emptyTask.Id, 0, map[string]string{"hostname": "worker.example"})); err != nil {
			t.Fatal(err)
		}
		got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: emptyTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if got.Logs[0].Metadata["hostname"] != "worker.example" {
			t.Fatalf("metadata after successful write=%v, want hostname=worker.example", got.Logs[0].Metadata)
		}

		nullTask := newTestTask("metadata-null", "metadata-null", testUser)
		if err := db.WriteEvent(ctx, events.NewTaskCreated(nullTask)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.client.Exec(ctx, `UPDATE tasks SET data=jsonb_set(data,'{logs,0,metadata}','null'::jsonb) WHERE id=$1`, nullTask.Id); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteEvent(ctx, events.NewMetadata(nullTask.Id, 0, map[string]string{"hostname": "worker.example"})); err != nil {
			t.Fatal(err)
		}
		got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: nullTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if got.Logs[0].Metadata["hostname"] != "worker.example" {
			t.Fatalf("metadata after replacing JSON null=%v, want hostname=worker.example", got.Logs[0].Metadata)
		}

		existingTask := newTestTask("metadata-existing", "metadata-existing", testUser)
		existingTask.Logs = []*tes.TaskLog{{Metadata: map[string]string{"rack": "west"}}}
		if err := db.WriteEvent(ctx, events.NewTaskCreated(existingTask)); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteEvent(ctx, events.NewMetadata(existingTask.Id, 0, map[string]string{"hostname": "worker.example"})); err != nil {
			t.Fatal(err)
		}
		got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: existingTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if got.Logs[0].Metadata["rack"] != "west" || got.Logs[0].Metadata["hostname"] != "worker.example" {
			t.Fatalf("metadata merge=%v, want rack=west and hostname=worker.example", got.Logs[0].Metadata)
		}

		reservedEvent := events.NewMetadata(existingTask.Id, 0, map[string]string{
			"must-not-apply":                 "value",
			events.ExecutionOwnerMetadataKey: "forged",
		})
		if err := db.WriteEvent(ctx, reservedEvent); err == nil {
			t.Fatal("metadata write containing the reserved execution-owner key succeeded")
		}
		got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: existingTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.Logs[0].Metadata["must-not-apply"]; ok {
			t.Fatalf("reserved mixed metadata partially applied: %v", got.Logs[0].Metadata)
		}
		if got.Logs[0].Metadata["rack"] != "west" || got.Logs[0].Metadata["hostname"] != "worker.example" {
			t.Fatalf("reserved metadata rejection changed existing metadata: %v", got.Logs[0].Metadata)
		}
	})

	t.Run("concurrent system log writes both persist", func(t *testing.T) {
		task := newTestTask("system-logs", "system-logs", testUser)
		if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
			t.Fatal(err)
		}

		tx, err := db.client.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Errorf("roll back row-lock barrier: %v", err)
			}
		}()
		if err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, task.Id).Scan(new(string)); err != nil {
			t.Fatal(err)
		}

		first := events.NewSystemLog(task.Id, 0, 0, "info", "first", nil)
		second := events.NewSystemLog(task.Id, 0, 0, "info", "second", nil)
		type result struct{ err error }
		done := make(chan result, 2)
		for _, event := range []*events.Event{first, second} {
			go func(event *events.Event) { done <- result{err: db.WriteEvent(ctx, event)} }(event)
		}

		barrierCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting int
			if err := db.client.QueryRow(barrierCtx, `
				SELECT count(*) FROM pg_stat_activity
				WHERE datname=current_database()
				  AND pid <> pg_backend_pid()
				  AND wait_event_type='Lock'
				  AND query ILIKE '%UPDATE tasks%'
			`).Scan(&waiting); err != nil {
				t.Fatalf("observe blocked writers: %v", err)
			}
			if waiting == 2 {
				break
			}
			select {
			case <-barrierCtx.Done():
				t.Fatalf("only %d system-log writers reached the row-lock barrier", waiting)
			case <-ticker.C:
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			select {
			case result := <-done:
				if result.err != nil {
					t.Fatal(result.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("system-log writer did not finish after row lock was released")
			}
		}

		got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]struct{}{first.SysLogString(): {}, second.SysLogString(): {}}
		actual := make(map[string]struct{}, len(got.Logs[0].SystemLogs))
		for _, log := range got.Logs[0].SystemLogs {
			actual[log] = struct{}{}
		}
		if len(actual) != 2 || len(got.Logs[0].SystemLogs) != 2 {
			t.Fatalf("persisted system logs=%v, want both successful writes", got.Logs[0].SystemLogs)
		}
		for log := range want {
			if _, ok := actual[log]; !ok {
				t.Errorf("persisted system logs %v omitted %q", got.Logs[0].SystemLogs, log)
			}
		}

		nullTask := newTestTask("system-logs-null", "system-logs-null", testUser)
		if err := db.WriteEvent(ctx, events.NewTaskCreated(nullTask)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.client.Exec(ctx, `UPDATE tasks SET data=jsonb_set(data,'{logs,0,system_logs}','null'::jsonb) WHERE id=$1`, nullTask.Id); err != nil {
			t.Fatal(err)
		}
		nullLog := events.NewSystemLog(nullTask.Id, 0, 0, "info", "after-null", nil)
		if err := db.WriteEvent(ctx, nullLog); err != nil {
			t.Fatal(err)
		}
		got, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: nullTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Logs[0].SystemLogs) != 1 || got.Logs[0].SystemLogs[0] != nullLog.SysLogString() {
			t.Fatalf("system logs after JSON null=%v, want the appended log", got.Logs[0].SystemLogs)
		}
	})

	t.Run("system log append targets its requested attempt", func(t *testing.T) {
		attemptTask := newTestTask("system-logs-attempt", "system-logs-attempt", testUser)
		attemptTask.Logs = []*tes.TaskLog{
			{SystemLogs: []string{"attempt-zero"}},
			{SystemLogs: []string{"attempt-one-existing"}},
		}
		if err := db.WriteEvent(ctx, events.NewTaskCreated(attemptTask)); err != nil {
			t.Fatal(err)
		}
		attemptLog := events.NewSystemLog(attemptTask.Id, 1, 0, "info", "attempt-one-appended", nil)
		if err := db.WriteEvent(ctx, attemptLog); err != nil {
			t.Fatal(err)
		}
		got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: attemptTask.Id, View: tes.View_FULL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Logs[0].SystemLogs) != 1 || got.Logs[0].SystemLogs[0] != "attempt-zero" {
			t.Fatalf("attempt 0 system logs=%v, want [attempt-zero]", got.Logs[0].SystemLogs)
		}
		if len(got.Logs[1].SystemLogs) != 2 || got.Logs[1].SystemLogs[0] != "attempt-one-existing" || got.Logs[1].SystemLogs[1] != attemptLog.SysLogString() {
			t.Fatalf("attempt 1 system logs=%v, want prior log plus appended event", got.Logs[1].SystemLogs)
		}
	})
}

func ids(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func postgresQAOwnerContext(t *testing.T) context.Context {
	t.Helper()
	var ownerContext context.Context
	_, err := server.NewAuthentication(nil, nil, server.AccessOwner).Interceptor(context.Background(), nil, nil,
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			ownerContext = ctx
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return ownerContext
}
