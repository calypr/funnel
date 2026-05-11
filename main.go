package main

import (
	"errors"
	"os"

	"github.com/ohsu-comp-bio/funnel/cmd"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/worker"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		logger.PrintSimpleError(err)

		// In the case of a K8s Executor error, do not error here: when the executor fails, the
		// worker catches the error and updates the task state, but the worker itself should not
		// fail. This avoids unwanted worker retries when the issue is a failure in the user's
		// script run by the executor.
		var execErr *worker.K8sExecutorErr
		if errors.As(err, &execErr) {
			os.Exit(0)
		}

		os.Exit(1)
	}
}
