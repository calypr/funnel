package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/stretchr/testify/mock"
)

// testNode wraps Node with some testing helpers.
type testNode struct {
	*NodeProcess
	Client *MockClient
	done   chan struct{}
}

func newTestNode(conf *config.Config, t *testing.T) testNode {
	workDir, _ := os.MkdirTemp("", "funnel-test-storage-")
	conf.Worker.WorkDir = workDir
	log := logger.NewLogger("test-node", logger.DebugConfig())

	// A mock scheduler client allows this code to fake/control the worker's
	// communication with a scheduler service.
	res, _ := detectResources(conf.Node, conf.Worker.WorkDir)
	s := new(MockClient)
	n := &NodeProcess{
		conf:      conf,
		client:    s,
		log:       log,
		resources: res,
		workerRun: NoopWorker,
		workers:   newRunSet(),
		timeout:   util.NewIdleTimeout(conf.Node.Timeout.GetDuration().AsDuration()),
		state:     NodeState_ALIVE,
	}

	s.On("PutNode", mock.Anything, mock.Anything).
		Return(nil, nil)
	s.On("PutNode", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.On("Close").Return(nil)

	return testNode{
		NodeProcess: n,
		Client:      s,
		done:        make(chan struct{}),
	}
}

func (t *testNode) Start() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	t.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(&Node{}, nil)
	go func() {
		t.NodeProcess.Run(ctx)
		close(t.done)
	}()
	return cancel
}

func (t *testNode) Wait() {
	<-t.done
}

func (t *testNode) AddTasks(ids ...string) {
	// Set up the scheduler mock to assign a task to the worker.
	t.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(&Node{
			TaskIds: ids,
		}, nil).
		Once()

	t.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(&Node{}, nil)
}

func timeLimit(t *testing.T, d time.Duration) func() {
	stop := make(chan struct{})
	errCh := make(chan error, 1) // Channel to report errors

	go func() {
		select {
		case <-time.NewTimer(d).C:
			errCh <- fmt.Errorf("time limit expired") // Send error
		case <-stop:
			return
		}
	}()

	// This is the cancel function that will be returned
	return func() {
		close(stop)
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err) // Report error from the main goroutine
			}
		default:
			// No error, do nothing
		}
	}
}
