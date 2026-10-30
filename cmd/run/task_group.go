package run

import (
	"fmt"
	"os"
	"sync"

	"context"

	"github.com/calypr/funnel/tes"
)

type taskGroup struct {
	wg        sync.WaitGroup
	err       chan error
	printTask bool
	client    *tes.Client
}

func (tg *taskGroup) runTask(t *tes.Task, wait bool, waitFor []string) {
	if tg.err == nil {
		tg.err = make(chan error)
	}

	tg.wg.Add(1)
	go func() {
		err := tg._run(t, wait, waitFor)
		if err != nil {
			tg.err <- err
		}
		tg.wg.Done()
	}()
}

func (tg *taskGroup) wait() error {
	done := make(chan struct{})
	go func() {
		tg.wg.Wait()
		close(done)
	}()

	select {
	case err := <-tg.err:
		return err
	case <-done:
		return nil
	}
}

func (tg *taskGroup) _run(task *tes.Task, wait bool, waitFor []string) error {

	if tg.printTask {
		// Marshal message to JSON
		taskJSON := tg.client.Marshaler.Format(task)
		if _, err := fmt.Fprintln(os.Stdout, taskJSON); err != nil {
			return fmt.Errorf("writing task output: %w", err)
		}
		return nil
	}

	if len(waitFor) > 0 {
		for _, tid := range waitFor {
			if err := tg.client.WaitForTask(context.Background(), tid); err != nil {
				if _, writeErr := fmt.Fprintf(os.Stderr, "[WARN] waiting for task %s: %v\n", tid, err); writeErr != nil {
					return fmt.Errorf("writing task wait warning: %w", writeErr)
				}
			}
		}
	}

	resp, rerr := tg.client.CreateTask(context.Background(), task)
	if rerr != nil {
		return rerr
	}

	taskID := resp.Id
	if _, err := fmt.Fprintln(os.Stdout, taskID); err != nil {
		return fmt.Errorf("writing created task ID: %w", err)
	}

	if wait {
		return tg.client.WaitForTask(context.Background(), taskID)
	}
	return nil
}
