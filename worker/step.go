package worker

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
)

type stepWorker struct {
	Conf    *config.Worker
	Command TaskCommand
	Event   *events.ExecutorWriter
	IP      string
}

func (s *stepWorker) Run(ctx context.Context) error {
	// WaitGroup to block closing the worker until the last event is written from events/executor.go:StreamLogTail()
	var wg sync.WaitGroup
	defer wg.Wait()
	s.Event.StartTime(time.Now())

	// subctx helps ensure that these goroutines are cleaned up,
	// even when the task is canceled.
	subctx, cleanup := context.WithCancel(context.Background())
	defer cleanup()

	done := make(chan error, 1)
	var stdout io.Writer
	var stderr io.Writer

	// Tail the stdout/err log streams.
	if s.Conf.LogTailSize > 0 {
		if s.Conf.LogUpdateRate != nil && s.Conf.LogUpdateRate.AsDuration() > 0 {
			stdout, stderr = s.Event.StreamLogTail(subctx, s.Conf.LogTailSize, s.Conf.LogUpdateRate.AsDuration(), &wg)
		} else {
			stdout, stderr = s.Event.LogTail(subctx, s.Conf.LogTailSize)
		}
	}

	// Capture stdout/err to file.
	out := s.Command.GetStdout()
	if out != nil && out != (*os.File)(nil) {
		stdout = io.MultiWriter(out, stdout)
		s.Command.SetStdout(stdout)
	}
	err := s.Command.GetStderr()
	if err != nil && err != (*os.File)(nil) {
		stderr = io.MultiWriter(err, stderr)
		s.Command.SetStderr(stderr)
	}

	s.Command.SetStdout(stdout)
	s.Command.SetStderr(stderr)

	go func() {
		done <- s.Command.Run(subctx)
	}()

	for {
		select {
		case <-ctx.Done():
			// Likely the task was canceled.
			s.Command.Stop()
			<-done
			s.Event.EndTime(time.Now())
			return ctx.Err()

		case result := <-done:
			s.Event.EndTime(time.Now())
			exitcode, err := getExitCode(result)
			if err != nil {
				s.Event.Error(err.Error())
			} else {
				s.Event.ExitCode(exitcode)
			}
			return result
		}
	}
}
