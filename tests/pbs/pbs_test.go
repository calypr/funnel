package pbs

import (
	"os"
	"testing"

	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
)

var fun *tests.Funnel
var serverName string

func TestMain(m *testing.M) {
	tests.ParseConfig()
	conf := tests.DefaultConfig()

	if conf.Compute != "pbs" {
		logger.Debug("Skipping PBS/Torque e2e tests...")
		os.Exit(0)
	}

	fun = tests.NewFunnel(conf)
	serverName = "funnel-test-server-" + tests.RandomString(6)
	fun.StartServerInDocker(serverName, "ohsucompbio/pbs-torque:latest", []string{"--hostname", "docker", "--privileged"})

	exit := 0
	defer func() {
		fun.CleanupTestServerContainer(serverName)
		os.Exit(exit)
	}()

	exit = m.Run()
	return
}

func TestHelloWorld(t *testing.T) {
	id := fun.Run(`
    --sh 'echo hello world'
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected task to have complete state; got:", task.State.String())
	}

	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout")
	}
}

func TestResourceRequest(t *testing.T) {
	id := fun.Run(`
    --sh 'echo I need resources!' --cpu 1 --ram 2 --disk 5
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected task to have complete state; got:", task.State.String())
	}

	if task.Logs[0].Logs[0].Stdout != "I need resources!\n" {
		t.Fatal("Missing stdout")
	}
}

// PBS/Torque seems to run the job regardless of memory or disk space requested
// func TestSubmitFail(t *testing.T) {
// 	id := fun.Run(`
//     --sh 'echo hello world' --ram 1000
//   `)
// 	task := fun.Wait(id)

// 	if task.State != tes.State_SYSTEM_ERROR {
// 		t.Fatal("expected task to have system error state; got:", task.State.String())
// 	}
// }
