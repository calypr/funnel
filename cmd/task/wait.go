package task

import (
	"context"

	"github.com/ohsu-comp-bio/funnel/tes"
)

// Wait runs the "task wait" CLI command, which polls the server,
// calling GetTask() for each ID, and blocking until the tasks have
// reached a terminal state.
func Wait(server string, ids []string) error {
	cli, err := tes.NewClient(server)
	if err != nil {
		return err
	}

	return cli.WaitForTask(context.Background(), ids...)
}
