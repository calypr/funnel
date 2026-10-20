package slurm

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
)

var fun *tests.Funnel
var serverName string

func TestMain(m *testing.M) {
	tests.ParseConfig()
	conf := tests.DefaultConfig()

	if conf.Compute != "slurm" {
		logger.Debug("Skipping slurm e2e tests...")
		os.Exit(0)
	}

	fun = tests.NewFunnel(conf)
	serverName = "funnel-test-server-" + tests.RandomString(6)
	fun.StartServerInDocker(serverName, "quay.io/ohsu-comp-bio/slurm:latest", []string{"--hostname", "slurmctl", "--cap-add", "sys_admin"})

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
		t.Fatal("expected task to be in complete state; got:", task.State.String())
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

func TestSubmitFail(t *testing.T) {
	id := fun.Run(`
    --sh 'echo hello world' --ram 1000
  `)
	task := fun.Wait(id)

	if task.State != tes.State_SYSTEM_ERROR {
		t.Fatal("expected task to be in system error state; got:", task.State.String())
	}
}

func TestCancel(t *testing.T) {
	id := fun.Run(`
    --sh 'echo I wont ever run!' --cpu 1000
  `)

	time.Sleep(time.Second)

	_, err := fun.HTTP.CancelTask(context.Background(), &tes.CancelTaskRequest{Id: id})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	task := fun.Wait(id)
	if task.State != tes.State_CANCELED {
		t.Error("expected task to have canceled state; got:", task.State.String())
	}

	bid := task.Logs[0].Metadata["slurm_id"]
	cmd := exec.Command("docker", "exec", serverName, "squeue", "--job", bid)
	out, err := cmd.Output()
	t.Log("cmd output:", string(out))
	t.Log("error:", err)
	if strings.Contains(string(out), bid) {
		t.Error("unexpected squeue output")
	}
}

func TestReconcile(t *testing.T) {
	id := fun.Run(`
    --sh 'echo I wont ever run!' --cpu 1000
  `)
	task := fun.Wait(id)

	if task.State != tes.State_SYSTEM_ERROR {
		t.Fatal("expected system error")
	}
}
