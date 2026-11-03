package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
)

type timeoutBackend struct{ events.Backend }

func (timeoutBackend) WriteEvent(ctx context.Context, _ *events.Event) error {
	<-ctx.Done()
	return ctx.Err()
}

type submissionTestStore struct {
	mu   sync.Mutex
	task *tes.Task
	logs []string
}

func (s *submissionTestStore) WriteEvent(ctx context.Context, e *events.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Type {
	case events.Type_TASK_CREATED:
		s.task = e.GetTask()
	case events.Type_TASK_STATE:
		s.task.State = e.GetState()
	case events.Type_SYSTEM_LOG:
		s.logs = append(s.logs, e.SysLogString())
	}
	return nil
}

func (*submissionTestStore) Close() {}

func TestSubmissionTimeoutRecordsTaskFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &submissionTestStore{}
		svc := &TaskService{Config: config.DefaultConfig(), Compute: timeoutBackend{}, Event: store, Log: logger.NewLogger("test", logger.DefaultConfig())}
		task := &tes.Task{Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}}
		response, err := svc.CreateTask(context.Background(), task)
		if err != nil || response.Id == "" {
			t.Fatalf("CreateTask = %v, %v", response, err)
		}
		svc.submitWG.Wait()
		if store.task.GetState() != tes.State_SYSTEM_ERROR {
			t.Errorf("task state = %s, want SYSTEM_ERROR", store.task.GetState())
		}
		if len(store.logs) != 1 || !strings.Contains(store.logs[0], "context deadline exceeded") {
			t.Errorf("system logs = %v, want submission timeout", store.logs)
		}
	})
}
