package task

import (
	"context"

	tesclient "github.com/calypr/funnel/client/tes"
)

// Wait runs the "task wait" CLI command, which polls the server,
// calling GetTask() for each ID, and blocking until the tasks have
// reached a terminal state.
func Wait(server string, ids []string) error {
	cli, err := tesclient.NewClient(server)
	if err != nil {
		return err
	}

	return cli.WaitForTask(context.Background(), ids...)
}
