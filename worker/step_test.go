package worker

import (
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"google.golang.org/protobuf/types/known/durationpb"
)

type logCommand struct{ Command }

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
