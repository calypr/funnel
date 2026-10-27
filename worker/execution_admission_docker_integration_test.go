package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/worker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestExecutionAdmissionDockerIntegration(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run real Docker execution admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	db, err := boltdb.NewBoltDB(&config.BoltDB{Path: filepath.Join(dir, "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "docker-admission", State: tes.Queued, Executors: []*tes.Executor{{Image: "alpine:latest", Command: []string{"sh", "-c", "echo executed >> /proof/launches"}}}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	var snapshots sync.WaitGroup
	snapshots.Add(2)
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		conf := config.DefaultConfig().Worker
		conf.WorkDir = t.TempDir()
		conf.PollingRate = durationpb.New(20 * time.Millisecond)
		// Avoid Docker name collisions as a second admission mechanism. The durable
		// database claim alone must prevent the second real container from running.
		conf.Container.RunCommand = "run --rm -v '" + dir + ":/proof' {{.Image}} {{.Command}}"
		reader := &admissionSnapshotReader{task: proto.Clone(task).(*tes.Task), snapshots: &snapshots, db: db}
		w := &worker.DefaultWorker{Conf: conf, Store: &storage.Mux{}, TaskReader: reader, EventWriter: db, ExecutionClaimer: db}
		workers.Add(1)
		go func() { defer workers.Done(); errs <- w.Run(ctx) }()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "launches"))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "executed\n"); count != 1 {
		t.Fatalf("real container executions=%d; want 1", count)
	}
	stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != tes.Complete {
		t.Fatalf("task state=%s; want COMPLETE", stored.State)
	}
}

type admissionSnapshotReader struct {
	db        tes.ReadOnlyServer
	task      *tes.Task
	snapshots *sync.WaitGroup
}

func (r *admissionSnapshotReader) Task(ctx context.Context) (*tes.Task, error) {
	r.snapshots.Done()
	ready := make(chan struct{})
	go func() { r.snapshots.Wait(); close(ready) }()
	select {
	case <-ready:
		return r.task, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (r *admissionSnapshotReader) State(ctx context.Context) (tes.State, error) {
	task, err := r.db.GetTask(ctx, &tes.GetTaskRequest{Id: r.task.Id, View: tes.View_MINIMAL.String()})
	return task.GetState(), err
}
func (*admissionSnapshotReader) Close() {}
