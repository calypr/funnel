package node

import (
	"fmt"

	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	workerCmd "github.com/calypr/funnel/cmd/worker"
	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/util"
)

// Run runs a node with the given config, blocking until the node exits.
func Run(ctx context.Context, conf *config.Config, log *logger.Logger) error {
	nodeID, err := scheduler.GenNodeID()
	if err != nil {
		return err
	}
	conf.Node.ID = nodeID

	factory := func(ctx context.Context, taskID string) error {
		w, err := workerCmd.NewWorker(ctx, conf, log, &workerCmd.Options{
			TaskID: taskID,
		})
		if err != nil {
			return err
		}
		err = w.Run(ctx)
		if err != nil {
			return fmt.Errorf("worker run failed: %w", err)
		}
		return nil
	}

	n, err := scheduler.NewNodeProcess(ctx, conf, factory, log)
	if err != nil {
		return err
	}

	_, cancel := context.WithCancel(context.Background())
	runctx := util.SignalContext(ctx, time.Nanosecond, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	hupsig := make(chan os.Signal, 1)
	go func() {
		for {
			select {
			case <-runctx.Done():
				return
			case <-hupsig:
				n.Drain()
			}
		}
	}()
	signal.Notify(hupsig, syscall.SIGHUP)

	if err := n.Run(runctx); err != nil {
		return fmt.Errorf("node run failed: %w", err)
	}

	return nil
}
