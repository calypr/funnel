package worker

import (
	"context"
	"fmt"
	"os/exec"
	"runtime/debug"
	"syscall"

	"github.com/calypr/funnel/version"
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

	if exiterr, exitOk := err.(*exec.ExitError); exitOk && exiterr.ProcessState != nil {
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
		cb(fmt.Errorf("panic: %v (version=%s commit=%s)\n%s", r, version.Version, version.GitCommit, debug.Stack()))
	}
}

// helper aims to simplify the error and context checking in the worker code.
type helper struct {
	syserr  error
	execerr error
	ctx     context.Context
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
