package server

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mountPathEventWriter struct {
	writes atomic.Int32
}

func (w *mountPathEventWriter) WriteEvent(context.Context, *events.Event) error {
	w.writes.Add(1)
	return nil
}

func (*mountPathEventWriter) Close() {}

type mountPathComputer struct {
	submissions atomic.Int32
}

func (c *mountPathComputer) WriteEvent(context.Context, *events.Event) error {
	c.submissions.Add(1)
	return nil
}

func (*mountPathComputer) CheckBackendParameterSupport(*tes.Task) error { return nil }
func (*mountPathComputer) Close()                                       {}

func TestCreateTaskRejectsKubernetesRootStreamBeforePersistence(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Compute = "k8s"
	writer := &mountPathEventWriter{}
	computer := &mountPathComputer{}
	log := logger.NewLogger("mount-validation-test", logger.DefaultConfig())
	log.Discard()
	service := &TaskService{Config: conf, Event: writer, Compute: computer, Log: log}

	_, err := service.CreateTask(context.Background(), &tes.Task{
		Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}, Stdout: "/capture"}},
	})
	if err == nil || status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "/outputs/") {
		t.Fatalf("CreateTask() error = %v, want InvalidArgument with a dedicated-directory workaround", err)
	}
	if got := writer.writes.Load(); got != 0 {
		t.Errorf("CreateTask() persisted %d events for a rejected task", got)
	}
	if got := computer.submissions.Load(); got != 0 {
		t.Errorf("CreateTask() dispatched %d rejected tasks", got)
	}
}

func TestCreateTaskLeavesIndependentGCPMountPolicyAlone(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Compute = "gcp-batch"
	writer := &mountPathEventWriter{}
	computer := &mountPathComputer{}
	log := logger.NewLogger("mount-validation-test", logger.DefaultConfig())
	log.Discard()
	service := &TaskService{Config: conf, Event: writer, Compute: computer, Log: log}

	if _, err := service.CreateTask(context.Background(), &tes.Task{
		Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}},
		Outputs:   []*tes.Output{{Path: "/result.json", Url: "file:///result.json", Type: tes.FileType_FILE}},
	}); err != nil {
		t.Fatalf("CreateTask() rejected the independent GCP backend task: %v", err)
	}
	service.Close()
	if got := writer.writes.Load(); got != 1 {
		t.Errorf("CreateTask() wrote %d events, want one", got)
	}
	if got := computer.submissions.Load(); got != 1 {
		t.Errorf("CreateTask() submitted %d tasks, want one", got)
	}
}

func TestMountExecutorBackendUsesPluginComputeSelection(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Compute = "local"
	service := &TaskService{Config: conf}

	ctx := context.WithValue(context.Background(), "pluginResponse", &proto.JobResponse{
		Config: &config.Config{Compute: "google"},
	})
	if got := service.mountExecutorBackend(ctx); got != "" {
		t.Fatalf("mountExecutorBackend() = %q, want independent GCP backend policy", got)
	}
}
