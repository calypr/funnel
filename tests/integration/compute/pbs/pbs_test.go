package pbs

import (
	"os"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/tests/internal/testenv"
)

var fun *testenv.Funnel
var serverName string

func TestMain(m *testing.M) {
	os.Exit(testenv.RunSuite(m, func(scope testenv.CleanupScope) bool {
		conf := testenv.DefaultConfig(scope)
		if conf.Compute != "pbs" {
			logger.Debug("Skipping PBS/Torque e2e testenv...")
			return false
		}

		fun = testenv.NewFunnel(scope, conf)
		serverName = "funnel-test-server-" + testenv.RandomString(6)
		scope.Cleanup(func() { fun.CleanupTestServerContainer(serverName) })
		fun.StartServerInDocker(serverName, "ohsucompbio/pbs-torque:latest", []string{"--hostname", "docker", "--privileged"})
		return true
	}))
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
