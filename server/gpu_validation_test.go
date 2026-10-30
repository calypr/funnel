package server

import (
	"context"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gpuTestComputer struct{ mountPathComputer }

func (*gpuTestComputer) BackendParameterKeys() []string { return []string{"gpu_count"} }
func (*gpuTestComputer) CheckBackendParameterSupport(task *tes.Task) error {
	if _, err := tes.GPUCount(task.GetResources()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

func TestCreateTaskRejectsInvalidBackendParametersBeforePersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]string
	}{
		{"invalid count", map[string]string{"gpu_count": "-1"}},
		{"unsupported key", map[string]string{"unknown": "1"}},
		{"case collision", map[string]string{"gpu_count": "1", "GPU_COUNT": "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer, computer := &mountPathEventWriter{}, &gpuTestComputer{}
			log := logger.NewLogger("gpu-validation", logger.DefaultConfig())
			log.Discard()
			service := &TaskService{Config: config.DefaultConfig(), Event: writer, Compute: computer, Log: log}
			_, err := service.CreateTask(context.Background(), &tes.Task{Resources: &tes.Resources{BackendParameters: tc.params, BackendParametersStrict: true}, Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("error=%v", err)
			}
			if writer.writes.Load() != 0 || computer.submissions.Load() != 0 {
				t.Fatal("invalid request persisted or submitted")
			}
		})
	}
}

func TestServiceInfoAdvertisesBackendParameters(t *testing.T) {
	for _, tc := range []struct {
		computer events.Computer
		want     int
	}{{&gpuTestComputer{}, 1}, {events.Backend{}, 0}} {
		service := &TaskService{Compute: tc.computer}
		info, err := service.GetServiceInfo(context.Background(), &tes.GetServiceInfoRequest{})
		if err != nil || len(info.GetTesResourcesBackendParameters()) != tc.want {
			t.Fatalf("info=%v error=%v", info, err)
		}
	}
}
