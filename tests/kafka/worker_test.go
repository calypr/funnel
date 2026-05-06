package kafka

import (
	"context"
	"os"
	"testing"

	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
)

var log = logger.NewLogger("kafka-worker-test", logger.DefaultConfig())
var fun *tests.Funnel
var conf *config.Config

func TestMain(m *testing.M) {
	tests.ParseConfig()
	conf = tests.DefaultConfig()
	conf.Compute = "noop"

	var active bool
	for _, val := range conf.EventWriters {
		if val == "kafka" {
			active = true
		}
	}

	if !active {
		logger.Debug("Skipping kafka e2e tests...")
		os.Exit(0)
	}

	fun = tests.NewFunnel(conf)
	fun.StartServer()

	os.Exit(m.Run())
}

func TestKafkaWorkerRun(t *testing.T) {
	tests.SetLogOutput(log, t)

	ctx := context.Background()
	// Task builder collects events into a task view.
	task := &tes.Task{}
	b := events.TaskBuilder{Task: task}
	l := &events.Logger{Log: log}
	m := &events.MultiWriter{b, l}

	// Read events from kafka, write into task builder.
	_, err := events.NewKafkaReader(ctx, conf.Kafka, m)
	if err != nil {
		t.Fatal(err)
	}

	// this only writes the task to the DB since the 'noop'
	// compute backend is in use
	id := fun.Run(`
    --sh 'echo hello world'
  `)

	err = workerCmd.Run(ctx, conf, log, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	fun.Wait(id)

	// Check the task (built from a stream of kafka events).
	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected state")
	}
}
