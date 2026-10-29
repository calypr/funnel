package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestGetTaskReturnsQueryError(t *testing.T) {
	db, err := NewPostgres(&config.Postgres{
		Host:     "localhost:5432",
		Database: "funnel",
		User:     "funnel",
		Password: "example",
		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{
				Duration: durationpb.New(5 * time.Second),
			},
		},
	})
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	db.Close()

	_, err = db.GetTask(getUserContext(), &tes.GetTaskRequest{Id: "missing"})
	if err == nil {
		t.Fatal("expected GetTask to return the closed pool error")
	}
	if errors.Is(err, tes.ErrNotFound) {
		t.Fatalf("GetTask returned ErrNotFound for a query failure: %v", err)
	}
}

func TestTrimUnusedExecutorLogs(t *testing.T) {
	cases := []struct {
		name string
		in   []*tes.ExecutorLog
		want int
	}{
		{
			name: "trims trailing never-run executor",
			in: []*tes.ExecutorLog{
				{StartTime: "2026-06-12T00:00:00Z", ExitCode: 127},
				{StartTime: ""}, // never ran
			},
			want: 1,
		},
		{
			name: "keeps all when every executor ran",
			in: []*tes.ExecutorLog{
				{StartTime: "2026-06-12T00:00:00Z"},
				{StartTime: "2026-06-12T00:00:01Z"},
			},
			want: 2,
		},
		{
			name: "trims all when none ran",
			in: []*tes.ExecutorLog{
				{StartTime: ""},
				{StartTime: ""},
			},
			want: 0,
		},
		{
			name: "does not trim a started executor preceding an empty one",
			in: []*tes.ExecutorLog{
				{StartTime: ""},
				{StartTime: "2026-06-12T00:00:00Z"},
			},
			want: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &tes.Task{Logs: []*tes.TaskLog{{Logs: tc.in}}}
			trimUnusedExecutorLogs(task)
			if got := len(task.Logs[0].Logs); got != tc.want {
				t.Errorf("expected %d executor logs, got %d", tc.want, got)
			}
		})
	}
}

func TestTrimUnusedExecutorLogs_NoLogs(t *testing.T) {
	task := &tes.Task{}
	trimUnusedExecutorLogs(task) // must not panic
}
