package postgres

import (
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
)

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
