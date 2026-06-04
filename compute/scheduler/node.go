package scheduler

import (
	"context"
	"os"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewNodeProcess returns a new Node instance
func NewNodeProcess(ctx context.Context, conf *config.Config, factory Worker, log *logger.Logger) (*NodeProcess, error) {
	log = log.WithFields("nodeID", conf.Node.ID)
	log.Debug("NewNode", "config", conf.Safe())

	cli, err := NewClient(ctx, conf.RPCClient)
	if err != nil {
		return nil, err
	}

	err = fsutil.EnsureDir(conf.Worker.WorkDir)
	if err != nil {
		return nil, err
	}

	// Detect available resources at startup
	res, derr := detectResources(conf.Node, conf.Worker.WorkDir)
	if derr != nil {
		log.Error("error detecting resources", "error", derr)
	}

	timeout := util.NewIdleTimeout(conf.Node.Timeout.GetDuration().AsDuration())
	state := NodeState_UNINITIALIZED

	return &NodeProcess{
		conf:      conf,
		client:    cli,
		log:       log,
		resources: res,
		workerRun: factory,
		workers:   newRunSet(),
		timeout:   timeout,
		state:     state,
		drained:   make(chan struct{}),
	}, nil
}

// NodeProcess is a structure used for tracking available resources on a compute resource.
type NodeProcess struct {
	conf      *config.Config
	client    Client
	log       *logger.Logger
	resources *Resources
	workerRun Worker
	workers   *runSet
	timeout   util.IdleTimeout
	state     NodeState
	drained   chan struct{}
}

// Run runs a node with the given config. This is responsible for communication
// with the server and starting task workers
func (n *NodeProcess) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	n.log.Info("Starting node")
	n.state = NodeState_ALIVE
	n.checkConnection(ctx)
	n.sync(ctx)

	ticker := time.NewTicker(n.conf.Node.UpdateRate.AsDuration())
	defer ticker.Stop()

	for {
		select {
		case <-n.timeout.Done():
			cancel()

		case <-n.drained:
			cancel()

		case <-ctx.Done():
			n.timeout.Stop()

			// The node gets 10 seconds to do a final sync with the scheduler.
			stopCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			n.state = NodeState_GONE
			n.sync(stopCtx)
			// close grpc client connection
			n.client.Close()

			// The workers get 10 seconds to finish up.
			n.workers.Wait(time.Second * 10)
			return nil

		case <-ticker.C:
			n.sync(ctx)
			n.checkIdleTimer()
			n.checkDrain()
		}
	}
}

func (n *NodeProcess) checkConnection(ctx context.Context) {
	_, err := n.client.GetNode(ctx, &GetNodeRequest{Id: n.conf.Node.ID})

	// If its a 404 error create a new node
	s, _ := status.FromError(err)
	if s.Code() != codes.NotFound {
		n.log.Error("Couldn't contact server.", err)
	} else {
		n.log.Info("Successfully connected to server.")
	}
}

// sync syncs the node's state with the server. It reports task state changes,
// handles signals from the server (new task, cancel task, etc), reports resources, etc.
//
// TODO Sync should probably use a channel to sync data access.
//
//	Probably only a problem for test code, where Sync is called directly.
func (n *NodeProcess) sync(ctx context.Context) {
	var r *Node
	var err error

	r, err = n.client.GetNode(ctx, &GetNodeRequest{Id: n.conf.Node.ID})
	if err != nil {
		// If its a 404 error create a new node
		s, _ := status.FromError(err)
		if s.Code() != codes.NotFound {
			n.log.Error("Couldn't get node state during sync.", err)
			return
		}
		n.log.Info("Starting initial node sync")
		r = &Node{Id: n.conf.Node.ID}
	}

	// Start task workers. runSet will track task IDs
	// to ensure there's only one worker per ID, so it's ok
	// to call this multiple times with the same task ID.
	for _, id := range r.TaskIds {
		if n.workers.Add(id) {
			go n.runTask(ctx, id)
		}
	}

	// Node data has been updated. Send back to server for database update.
	var derr error
	n.resources, derr = detectResources(n.conf.Node, n.conf.Worker.WorkDir)
	if derr != nil {
		n.log.Error("error detecting resources", "error", derr)
	}

	// Merge metadata
	meta := map[string]string{}
	for k, v := range n.conf.Node.Metadata {
		meta[k] = v
	}
	for k, v := range r.GetMetadata() {
		meta[k] = v
	}

	_, err = n.client.PutNode(context.Background(), &Node{
		Id:        n.conf.Node.ID,
		Resources: n.resources,
		State:     n.state,
		Version:   r.GetVersion(),
		Metadata:  meta,
		Hostname:  hostname(),
		TaskIds:   r.TaskIds,
	})
	if err != nil {
		n.log.Error("Couldn't save node update. Recovering.", err)
	}
}

func (n *NodeProcess) runTask(ctx context.Context, id string) {
	log := n.log.WithFields("ns", "worker", "taskID", id)
	log.Info("Running task")

	defer n.workers.Remove(id)
	defer func() {
		// task cannot fully complete until it has successfully removed the
		// assigned ID from the node database. this helps prevent tasks from
		// running multiple times.
		ticker := time.NewTicker(n.conf.Node.UpdateRate.AsDuration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r, err := n.client.GetNode(ctx, &GetNodeRequest{Id: n.conf.Node.ID})
				if err != nil {
					log.Error("couldn't get node state during task sync.", err)
					// break out of "select", but not "for".
					// i.e. try again later
					break
				}

				// Find the finished task ID in the node's assigned IDs and remove it.
				var ids []string
				for _, tid := range r.TaskIds {
					if tid != id {
						ids = append(ids, tid)
					}
				}
				r.TaskIds = ids

				_, err = n.client.PutNode(ctx, r)
				if err != nil {
					log.Error("couldn't save node update during task sync.", err)
					// break out of "select", but not "for".
					// i.e. try again later
					break
				}
				// Update was successful, return.
				return
			}
		}
	}()

	defer func() {
		if r := recover(); r != nil {
			log.Error("caught panic task worker", r)
		}
	}()

	err := n.workerRun(ctx, id)
	if err != nil {
		log.Error("error running task", err)
		return
	}

	log.Info("Task complete")
}

// Check if the node is idle. If so, start the timeout timer.
func (n *NodeProcess) checkIdleTimer() {
	// The pool is idle if there are no task workers.
	// The pool should not time out if it's not alive (e.g. if it's initializing)
	idle := n.workers.Count() == 0 && n.state == NodeState_ALIVE
	if idle {
		n.timeout.Start()
	} else {
		n.timeout.Stop()
	}
}

func (n *NodeProcess) checkDrain() {
	if n.workers.Count() == 0 && n.state == NodeState_DRAIN {
		close(n.drained)
	}
}

// Drain sets the node state to DRAIN.
func (n *NodeProcess) Drain() {
	n.state = NodeState_DRAIN
}

func hostname() string {
	if name, err := os.Hostname(); err == nil {
		return name
	}
	return ""
}
