package schedulercontract

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/worker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func testConcurrentExecutionWorkers(t *testing.T, open storeFactory) {
	db := open(t)
	ctx := testContext()
	task := createTask(t, db, ctx, "worker-race")
	dir := t.TempDir()
	marker := filepath.Join(dir, "launches")
	driver := filepath.Join(dir, "driver")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nversion) printf '{\"Server\":\"1\",\"Client\":\"1\"}' ;;\nrun) printf 'launch\\n' >> '%s' ;;\nesac\n", marker)
	if err := os.WriteFile(driver, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	barrier := newRendezvous(t, "worker task snapshots")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		conf := config.DefaultConfig().Worker
		conf.WorkDir = t.TempDir()
		conf.PollingRate = durationpb.New(20 * time.Millisecond)
		conf.Container.DriverCommand = driver
		conf.Container.PullCommand = "pull"
		conf.Container.RunCommand = "run"
		reader := &executionSnapshotReader{db: db, id: task.Id, barrier: barrier}
		w := &worker.DefaultWorker{Conf: conf, Store: &storage.Mux{}, TaskReader: reader, EventWriter: db, ExecutionClaimer: db.(events.ExecutionClaimer)}
		wg.Add(1)
		go func() { defer wg.Done(); errs <- w.Run(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "launch\n"); lines != 1 {
		t.Fatalf("executor launches = %d; want 1", lines)
	}
	if state := getTask(t, db, ctx, task.Id).State; state != tes.Complete {
		t.Fatalf("losing worker changed winner state: %s", state)
	}
}

type executionSnapshotReader struct {
	db      store
	id      string
	barrier *rendezvous
}

func (r *executionSnapshotReader) Task(ctx context.Context) (*tes.Task, error) {
	task, err := r.db.GetTask(ctx, &tes.GetTaskRequest{Id: r.id, View: tes.View_FULL.String()})
	if err == nil && r.barrier != nil {
		r.barrier.wait()
	}
	return task, err
}
func (r *executionSnapshotReader) State(ctx context.Context) (tes.State, error) {
	task, err := r.db.GetTask(ctx, &tes.GetTaskRequest{Id: r.id, View: tes.View_MINIMAL.String()})
	return task.GetState(), err
}
func (*executionSnapshotReader) Close() {}

func testLostExecutionResponse(t *testing.T, open storeFactory) {
	db := open(t)
	ctx := testContext()
	task := createTask(t, db, ctx, "lost-execution-response")
	claimer := db.(events.ExecutionClaimer)
	lost := errors.New("injected lost response after execution claim committed")
	w := &worker.DefaultWorker{
		Conf:        config.DefaultConfig().Worker,
		TaskReader:  &executionSnapshotReader{db: db, id: task.Id},
		EventWriter: db,
		ExecutionClaimer: executionClaimCall(func(ctx context.Context, id string) (bool, error) {
			acquired, err := claimer.ClaimExecution(ctx, id)
			if err != nil || !acquired {
				return acquired, err
			}
			return false, lost
		}),
	}
	if err := w.Run(ctx); !errors.Is(err, lost) {
		t.Fatalf("lost response worker=%v", err)
	}
	before := getTask(t, db, ctx, task.Id)
	if before.State != tes.Running {
		t.Fatalf("lost response changed committed state: %s", before.State)
	}
	retryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.TaskReader = &cancelExecutionObservation{TaskReader: &executionSnapshotReader{db: db, id: task.Id}, cancel: cancel}
	w.ExecutionClaimer = claimer
	if err := w.Run(retryCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("retry observer=%v", err)
	}
	after := getTask(t, db, ctx, task.Id)
	if !proto.Equal(before, after) {
		t.Fatalf("lost response/retry changed winner logs/state: before=%v after=%v", before, after)
	}
	if acquired, err := claimer.ClaimExecution(ctx, task.Id); err != nil || acquired {
		t.Fatalf("lost response permitted another execution: %v,%v", acquired, err)
	}
}

type executionClaimCall func(context.Context, string) (bool, error)

func (f executionClaimCall) ClaimExecution(ctx context.Context, id string) (bool, error) {
	return f(ctx, id)
}

type cancelExecutionObservation struct {
	worker.TaskReader
	cancel context.CancelFunc
}

func (r *cancelExecutionObservation) State(ctx context.Context) (tes.State, error) {
	state, err := r.TaskReader.State(ctx)
	r.cancel()
	return state, err
}
