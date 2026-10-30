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
	if err := ctx.Err(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	_ = s.Event.StartTime(time.Now()) //nolint:errcheck // Timing metadata must not change executor execution.

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

	commandCtx := subctx
	var cancelCommand context.CancelFunc
	if _, ok := s.Command.(*DockerCommand); ok {
		commandCtx, cancelCommand = context.WithCancel(context.WithoutCancel(ctx))
		defer cancelCommand()
	}
	go func() {
		defer handlePanic(func(err error) { done <- err })
		done <- s.Command.Run(commandCtx)
	}()

	for {
		select {
		case <-ctx.Done():
			stopErr := s.Command.Stop()
			if stopErr != nil {
				_ = s.Event.Error("failed to stop executor after cancellation", stopErr) //nolint:errcheck // Cancellation remains the task result when reporting stop failure.
			}
			var result error
			if cancelCommand != nil && stopErr == nil {
				select {
				case result = <-done:
				case <-time.After(5 * time.Second):
					cancelCommand()
					result = <-done
				}
			} else {
				if cancelCommand != nil {
					cancelCommand()
				}
				result = <-done
			}
			if code, err := getExitCode(result); err == nil && code >= 0 {
				_ = s.Event.ExitCode(code) //nolint:errcheck // Exit-code logging must not replace cancellation.
			}
			_ = s.Event.EndTime(time.Now()) //nolint:errcheck // Timing metadata must not change the cancellation result.
			return ctx.Err()

		case result := <-done:
			if ctx.Err() != nil {
				err := s.Command.Stop()
				if err != nil {
					_ = s.Event.Error("failed to stop executor after cancellation", err) //nolint:errcheck // Cancellation remains the task result when reporting stop failure.
				}
				if code, err := getExitCode(result); err == nil && code >= 0 {
					_ = s.Event.ExitCode(code) //nolint:errcheck // Exit-code logging must not replace cancellation.
				}
				_ = s.Event.EndTime(time.Now()) //nolint:errcheck // Timing metadata must not change the cancellation result.
				return ctx.Err()
			}
			_ = s.Event.EndTime(time.Now()) //nolint:errcheck // Timing metadata must not change executor execution.
			exitcode, err := getExitCode(result)
			if err != nil {
				_ = s.Event.Error(err.Error()) //nolint:errcheck // The command error is returned below; logging must not replace it.
			} else {
				_ = s.Event.ExitCode(exitcode) //nolint:errcheck // Exit-code logging must not change executor execution.
			}
			return result
		}
	}
}
