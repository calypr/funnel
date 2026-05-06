package node

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/util"
)

// Run runs a node with the given config, blocking until the node exits.
func Run(ctx context.Context, conf *config.Config, log *logger.Logger) error {
	conf.Node.ID = scheduler.GenNodeID()

	factory := func(ctx context.Context, taskID string) error {
		w, err := workerCmd.NewWorker(ctx, conf, log, &workerCmd.Options{
			TaskID: taskID,
		})
		if err != nil {
			return err
		}
		w.Run(ctx)
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

	n.Run(runctx)

	return nil
}
