package run

import (
	"fmt"
	"sync"

	"context"

	"github.com/ohsu-comp-bio/funnel/tes"
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
		fmt.Println(taskJSON)
		return nil
	}

	if len(waitFor) > 0 {
		for _, tid := range waitFor {
			tg.client.WaitForTask(context.Background(), tid)
		}
	}

	resp, rerr := tg.client.CreateTask(context.Background(), task)
	if rerr != nil {
		return rerr
	}

	taskID := resp.Id
	fmt.Println(taskID)

	if wait {
		return tg.client.WaitForTask(context.Background(), taskID)
	}
	return nil
}
