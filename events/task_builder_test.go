package events

import (
	"context"
	"sync"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
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
	if got := len(builder.Task.GetTaskLog(0).SystemLogs); got != 512 {
		t.Fatalf("system logs = %d, want 512", got)
	}
	for i := range 32 {
		if got := builder.Task.GetExecLog(0, i).Stdout; got != "complete output" {
			t.Errorf("executor %d stdout = %q, want complete output", i, got)
		}
	}
}
