package task

import (
	"context"

	"github.com/calypr/funnel/internal/cli/tesclient"
)

// Wait runs the "task wait" CLI command, which polls the server,
// calling GetTask() for each ID, and blocking until the tasks have
// reached a terminal state.
func Wait(server string, ids []string) error {
	cli, err := tesclient.New(server)
	if err != nil {
		return err
	}

	return cli.WaitForTask(context.Background(), ids...)
}
