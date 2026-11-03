package worker

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
)

type panicCommand struct{ Command }

func (*panicCommand) Run(context.Context) error { panic("executor isolation probe") }

func TestExecutorPanicDoesNotCrashWorker(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_EXECUTOR_PANIC") == "1" {
		step := &stepWorker{Conf: &config.Worker{}, Command: &panicCommand{}, Event: events.NewExecutorWriter("panic-task", 0, 0, events.Noop{})}
		if err := step.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "executor isolation probe") {
			t.Fatalf("expected returned panic diagnostic, got %v", err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorPanicDoesNotCrashWorker$")
	cmd.Env = append(os.Environ(), "FUNNEL_TEST_EXECUTOR_PANIC=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(output), "executor isolation probe") {
			t.Fatalf("unexpected child failure: %v\n%s", err, output)
		}
		t.Fatalf("executor panic crashed worker process: %v\n%s", err, output)
	}
}
