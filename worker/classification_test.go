package worker

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"testing/synctest"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
)

// TestExecutorErrorClassification pins the exit-code-based classification of
// local executor failures: errors carrying an exit code (the container ran
// and exited) are executor errors; errors without one (docker driver missing,
// failed to start) are system errors.
func TestExecutorErrorClassification(t *testing.T) {
	cmd := exec.Command("false")
	if runErr := cmd.Run(); runErr == nil {
		t.Fatal("expected false to exit nonzero")
	} else {
		exitErr := runErr.(*exec.ExitError)
		if _, codeErr := getExitCode(exitErr); codeErr != nil {
			t.Fatalf("exec.ExitError should yield an exit code: %v", codeErr)
		}
	}

	// A bare ExitError without ProcessState must not panic.
	if _, codeErr := getExitCode(&exec.ExitError{}); codeErr == nil {
		t.Fatal("ExitError without ProcessState should not classify as an executor error")
	}

	plainErr := errors.New("docker driver binary not found")
	if _, codeErr := getExitCode(plainErr); codeErr == nil {
		t.Fatal("error without an exit code should not be classified as an executor error")
	}
}

// hangCommand only returns when its own context is canceled, and its Stop is
// a no-op — simulating a driver whose stop command fails to kill it.
type hangCommand struct {
	Command
}

func (c *hangCommand) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *hangCommand) Stop() error { return nil }

// TestStepRunCancelsCommandWithCallerContext verifies that a running executor
// command is canceled when the caller's context ends, even if the command's
// Stop is a no-op. subctx must be rooted at the caller's context; rooting it
// at context.Background() leaked the command goroutine and hung Run.
func TestStepRunCancelsCommandWithCallerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		step := &stepWorker{
			Conf:    &config.Worker{},
			Command: &hangCommand{},
			Event:   events.NewExecutorWriter("task", 0, 0, &noopWriter{}),
		}
		done := make(chan error, 1)
		go func() { done <- step.Run(ctx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("step.Run did not return after the caller's context was canceled")
		}
	})
}

type noopWriter struct{}

func (w *noopWriter) WriteEvent(_ context.Context, _ *events.Event) error { return nil }
func (w *noopWriter) Close()                                              {}
