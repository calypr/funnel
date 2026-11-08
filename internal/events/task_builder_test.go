package events

import (
	"context"
	"sync"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/taskmodel"
)

func TestTaskBuilderConcurrentEvents(t *testing.T) {
	builder := &TaskBuilder{Task: &tes.Task{}}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 16 {
				if err := builder.WriteEvent(context.Background(), NewSystemLog("task", 0, 0, "info", "concurrent log", nil)); err != nil {
					t.Error(err)
				}
			}
			if err := builder.WriteEvent(context.Background(), NewStdout("task", 0, uint32(i), "complete output")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := len(taskmodel.GetTaskLog(builder.Task, 0).SystemLogs); got != 512 {
		t.Fatalf("system logs = %d, want 512", got)
	}
	for i := range 32 {
		if got := taskmodel.GetExecLog(builder.Task, 0, i).Stdout; got != "complete output" {
			t.Errorf("executor %d stdout = %q, want complete output", i, got)
		}
	}
}
