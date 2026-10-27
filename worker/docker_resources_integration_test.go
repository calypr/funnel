package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func TestDockerResourceLimitsIntegration(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}

	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enforcement_%t", enabled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			id := fmt.Sprintf("funnel-resource-test-%d", time.Now().UnixNano())
			name := id + "-0"
			t.Cleanup(func() {
				out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
				if err != nil && !strings.Contains(string(out), "No such container") {
					t.Errorf("remove container: %v: %s", err, out)
				}
			})

			conf := config.DefaultConfig().Worker
			conf.Container.EnforceResources = enabled
			conf.Container.PullCommand = "image inspect {{.Image}}"
			conf.Container.RunCommand = strings.Replace(conf.Container.RunCommand, "{{if .RemoveContainer}}--rm{{end}}", "", 1)
			w := &DefaultWorker{Conf: conf, Executor: Executor{Backend: "docker"}}
			task := &tes.Task{
				Id:        id,
				Resources: &tes.Resources{CpuCores: 2, RamGb: 0.125},
				Executors: []*tes.Executor{{Image: "alpine:latest", Command: []string{"true"}}},
			}
			run := &helper{}
			w.runExecutors(ctx, events.NewTaskWriter(id, 0, events.Noop{}), NewFileMapper(t.TempDir()), task, run)
			if !run.ok() {
				t.Fatalf("worker failed: system=%v executor=%v", run.syserr, run.execerr)
			}

			out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .HostConfig}}", name).CombinedOutput()
			if err != nil {
				t.Fatalf("inspect container: %v: %s", err, out)
			}
			var got struct {
				NanoCPUs   int64
				Memory     int64
				MemorySwap int64
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode container settings: %v", err)
			}
			var cpu, memory int64
			if enabled {
				cpu, memory = 2_000_000_000, 128*1024*1024
			}
			if got.NanoCPUs != cpu || got.Memory != memory || got.MemorySwap != memory {
				t.Fatalf("Docker limits = %+v, want CPU %d, memory %d, memory+swap %d", got, cpu, memory, memory)
			}
		})
	}
}
