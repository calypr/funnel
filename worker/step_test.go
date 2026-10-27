package worker

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"google.golang.org/protobuf/types/known/durationpb"
)

type logCommand struct{ Command }

type cancellationCommand struct {
	Command
	started chan struct{}
	stopped atomic.Bool
}

func (c *cancellationCommand) Run(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func (c *cancellationCommand) Stop() error {
	c.stopped.Store(true)
	return nil
}

func TestStepStopsCommandWhenCancellationAndCompletionRace(t *testing.T) {
	for i := 0; i < 10000; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		command := &cancellationCommand{started: make(chan struct{})}
		step := &stepWorker{Conf: &config.Worker{}, Command: command, Event: events.NewExecutorWriter("task", 0, 0, events.Noop{})}
		done := make(chan error, 1)
		go func() { done <- step.Run(ctx) }()
		<-command.started
		cancel()
		if err := <-done; err != context.Canceled {
			t.Fatalf("iteration %d: got %v, want context canceled", i, err)
		}
		if !command.stopped.Load() {
			t.Fatalf("iteration %d: canceled command was not stopped", i)
		}
	}
}

type completedCommand struct {
	Command
	stopped bool
}

func (c *completedCommand) Run(context.Context) error { return nil }
func (c *completedCommand) Stop() error {
	c.stopped = true
	return nil
}

func TestStepDoesNotStopCompletedCommand(t *testing.T) {
	command := &completedCommand{}
	step := &stepWorker{Conf: &config.Worker{}, Command: command, Event: events.NewExecutorWriter("task", 0, 0, events.Noop{})}
	if err := step.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if command.stopped {
		t.Fatal("normally completed command was stopped")
	}
}

func (c *logCommand) Run(context.Context) error {
	if _, err := io.WriteString(c.Stdout, "stdout tail"); err != nil {
		return err
	}
	_, err := io.WriteString(c.Stderr, "stderr tail")
	return err
}

func (c *logCommand) Stop() error { return nil }

type blockedLogWriter struct {
	release <-chan struct{}
	logs    chan *events.Event
}

func (w *blockedLogWriter) WriteEvent(_ context.Context, event *events.Event) error {
	if event.Type == events.Type_EXECUTOR_STDOUT || event.Type == events.Type_EXECUTOR_STDERR {
		<-w.release
		w.logs <- event
	}
	return nil
}

func (w *blockedLogWriter) Close() {}

func TestStepWaitsForFinalLogs(t *testing.T) {
	for _, interval := range []time.Duration{0, time.Hour} {
		t.Run(interval.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				writer := &blockedLogWriter{release: release, logs: make(chan *events.Event, 4)}
				step := &stepWorker{
					Conf:    &config.Worker{LogTailSize: 100, LogUpdateRate: durationpb.New(interval)},
					Command: &logCommand{},
					Event:   events.NewExecutorWriter("task", 0, 0, writer),
				}
				done := make(chan error, 1)
				go func() { done <- step.Run(context.Background()) }()
				synctest.Wait()
				returned := false
				select {
				case err := <-done:
					returned = true
					t.Errorf("Run returned before logs finished, error = %v", err)
				default:
				}
				close(release)
				if !returned {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				close(writer.logs)
				var stdout, stderr string
				for event := range writer.logs {
					switch event.Type {
					case events.Type_EXECUTOR_STDOUT:
						stdout = event.GetStdout()
					case events.Type_EXECUTOR_STDERR:
						stderr = event.GetStderr()
					}
				}
				if stdout != "stdout tail" || stderr != "stderr tail" {
					t.Errorf("logs = (%q, %q), want (stdout tail, stderr tail)", stdout, stderr)
				}
			})
		})
	}
}
