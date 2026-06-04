package worker

import (
	"context"
	"fmt"
	"os/exec"
	"runtime/debug"
	"syscall"
)

// getExitCode gets the exit status (i.e. exit code) from the result of an executed command.
// The exit code is zero if the command completed without error.
func getExitCode(err error) (int, error) {
	// The error is nil, the command returned successfully, so exit status is 0.
	if err == nil {
		return 0, nil
	}

	// Check for Kubernetes error first
	if k8sErr, ok := err.(*K8sExecutorErr); ok {
		return k8sErr.ExitCode, nil
	}

	if exiterr, exitOk := err.(*exec.ExitError); exitOk {
		if status, statusOk := exiterr.Sys().(syscall.WaitStatus); statusOk {
			return status.ExitStatus(), nil
		}
	}

	// Default to exit code 1 for any other errors.
	return 1, fmt.Errorf("failed to get exit code: %w", err)
}

// recover from panic and call "cb" with an error value.
func handlePanic(cb func(error)) {
	if r := recover(); r != nil {
		if e, ok := r.(error); ok {
			b := debug.Stack()
			cb(fmt.Errorf("panic: %s\n%s", e, string(b)))
		} else {
			cb(fmt.Errorf("Unknown worker panic: %+v", r))
		}
	}
}

// helper aims to simplify the error and context checking in the worker code.
type helper struct {
	syserr       error
	execerr      error
	taskCanceled bool
	ctx          context.Context
}

func (h *helper) ok() bool {
	if h.ctx != nil {
		// Check if the context is done, but don't block waiting on it.
		select {
		case <-h.ctx.Done():
			h.syserr = h.ctx.Err()
		default:
		}
	}
	return h.syserr == nil && h.execerr == nil
}
