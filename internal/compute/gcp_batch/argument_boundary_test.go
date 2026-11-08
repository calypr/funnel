package gcp_batch

import (
	"testing"

	"cloud.google.com/go/batch/apiv1/batchpb"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/internal/logger"
	logconfig "github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
)

func TestSubmitRejectsAmbiguousContainerWorkdirBeforeDispatch(t *testing.T) {
	for _, workdir := range []string{"/work --privileged", "/work\n--privileged", "/work'quoted'", "/work;flag", "/work$(flag)"} {
		t.Run(workdir, func(t *testing.T) {
			dispatched := false
			backend := &Backend{
				conf: &config.GCPBatch{Project: "project", Location: "region"},
				log:  logger.NewLogger("test", logconfig.DefaultConfig()), event: &noopEventWriter{},
				client: &mockClient{CreateJobFunc: func(*batchpb.CreateJobRequest) (*batchpb.Job, error) { dispatched = true; return &batchpb.Job{}, nil }},
			}
			task := &tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}, Workdir: workdir}}}
			if err := backend.Submit(task); err == nil {
				t.Fatal("accepted ambiguous container options")
			}
			if dispatched {
				t.Fatal("dispatched unsafe container options to provider")
			}
		})
	}
}
