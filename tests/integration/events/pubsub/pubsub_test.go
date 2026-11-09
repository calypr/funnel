package pubsub

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	workerCmd "github.com/calypr/funnel/internal/cli/worker"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/tests/internal/testenv"
)

var log = logger.NewLogger("pubsub-worker-test", logger.DefaultConfig())
var fun *testenv.Funnel
var conf *config.Config

func TestMain(m *testing.M) {
	os.Exit(testenv.RunSuite(m, func(scope testenv.CleanupScope) bool {
		conf = testenv.DefaultConfig(scope)
		conf.Compute = "noop"

		for _, val := range conf.EventWriters {
			if val == "pubsub" {
				fun = testenv.NewFunnel(scope, conf)
				fun.StartServer()
				return true
			}
		}

		logger.Debug("Skipping pubsub e2e testenv...")
		return false
	}))
}

func TestPubSubWorkerRun(t *testing.T) {
	testenv.SetLogOutput(log, t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Task builder collects events into a task view.
	task := &tes.Task{}
	b := &events.TaskBuilder{Task: task}

	var result error

	// Read events from pubsub, write into task builder.
	subname := "test-pubsub-" + testenv.RandomString(10)
	go func() {
		err := events.ReadPubSub(ctx, conf.PubSub, subname, b)
		if err != nil {
			result = errors.Join(result, err)
		}
	}()

	// this only writes the task to the DB since the 'noop'
	// compute backend is in use
	id := fun.Run(`'echo hello world'`)

	err := workerCmd.Run(ctx, conf, nil, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	fun.Wait(id)
	time.Sleep(time.Second)

	if result != nil {
		t.Fatal(result)
	}

	// Check the task (built from a stream of kafka events).
	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected state", task.State)
	}
}
