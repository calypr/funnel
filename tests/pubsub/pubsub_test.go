package pubsub

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"
	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
)

var log = logger.NewLogger("pubsub-worker-test", logger.DefaultConfig())
var fun *tests.Funnel
var conf *config.Config

func TestMain(m *testing.M) {
	tests.ParseConfig()
	conf = tests.DefaultConfig()
	conf.Compute = "noop"

	var active bool
	for _, val := range conf.EventWriters {
		if val == "pubsub" {
			active = true
		}
	}

	if !active {
		logger.Debug("Skipping pubsub e2e tests...")
		os.Exit(0)
	}

	fun = tests.NewFunnel(conf)
	fun.StartServer()

	os.Exit(m.Run())
}

func TestPubSubWorkerRun(t *testing.T) {
	tests.SetLogOutput(log, t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Task builder collects events into a task view.
	task := &tes.Task{}
	b := events.TaskBuilder{Task: task}

	var result *multierror.Error

	// Read events from pubsub, write into task builder.
	subname := "test-pubsub-" + tests.RandomString(10)
	go func() {
		err := events.ReadPubSub(ctx, conf.PubSub, subname, b)
		if err != nil {
			result = multierror.Append(result, err)
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
