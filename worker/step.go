package worker

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
)

type stepWorker struct {
	Conf    *config.Worker
	Command TaskCommand
	Event   *events.ExecutorWriter
	IP      string
}

func (s *stepWorker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	_ = s.Event.StartTime(time.Now())

	// subctx is rooted at the caller's context so the command goroutine is
	// cleaned up both when the task is canceled and if this Run exits while
	// the command is still running. cleanup() covers the normal-return path.
	subctx, cleanup := context.WithCancel(ctx)
	defer cleanup()

	done := make(chan error, 1)
	var stdout io.Writer
	var stderr io.Writer

	// Tail the stdout/err log streams.
	if s.Conf.LogTailSize > 0 {
		if s.Conf.LogUpdateRate != nil && s.Conf.LogUpdateRate.AsDuration() > 0 {
			stdout, stderr = s.Event.StreamLogTail(subctx, s.Conf.LogTailSize, s.Conf.LogUpdateRate.AsDuration(), &wg)
		} else {
			stdout, stderr = s.Event.LogTail(subctx, s.Conf.LogTailSize, &wg)
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
			_ = s.Command.Stop()
			<-done
			_ = s.Event.EndTime(time.Now())
			return ctx.Err()

		case result := <-done:
			if ctx.Err() != nil {
				_ = s.Command.Stop()
				_ = s.Event.EndTime(time.Now())
				return ctx.Err()
			}
			_ = s.Event.EndTime(time.Now())
			exitcode, err := getExitCode(result)
			if err != nil {
				_ = s.Event.Error(err.Error())
			} else {
				_ = s.Event.ExitCode(exitcode)
			}
			return result
		}
	}
}
