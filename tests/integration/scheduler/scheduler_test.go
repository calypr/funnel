package scheduler

import (
	"os"
	"testing"
	"time"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/tests/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ParseConfig()
	os.Exit(m.Run())
}

func TestReadQueue(t *testing.T) {
	c := testenv.DefaultConfig(t)
	c.Compute = "manual"
	f := testenv.NewFunnel(t, c)
	f.StartServer()

	for i := 0; i < 10; i++ {
		f.Run(`--sh 'echo 1'`)
	}
	time.Sleep(time.Second * 5)

	tasks := f.Scheduler.Queue.ReadQueue(10, nil)

	if len(tasks) != 10 {
		t.Error("unexpected task count", len(tasks))
	}

	min := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}

	// test that read queue returns tasks in first in first out order
	for i := range tasks {
		j := min(i+1, len(tasks)-1)
		if tasks[i].CreationTime > tasks[j].CreationTime {
			t.Error("unexpected task sort order")
		}
	}
}

func TestCancel(t *testing.T) {
	c := testenv.DefaultConfig(t)
	c.Compute = "manual"
	f := testenv.NewFunnel(t, c)
	f.StartServer()

	id := f.Run(`'sleep 1000'`)
	err := f.Cancel(id)
	if err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	task := f.Get(id)
	if task.State != tes.Canceled {
		t.Error("expected canceled state")
	}
}
