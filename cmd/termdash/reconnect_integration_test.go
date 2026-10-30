package termdash

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

type reconnectReader struct{}

func (reconnectReader) ListTasks(_ context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	if req.State != tes.State_UNKNOWN && req.State != tes.State_RUNNING {
		return &tes.ListTasksResponse{}, nil
	}
	return &tes.ListTasksResponse{Tasks: []*tes.Task{{Id: "restart-task", State: tes.Running, CreationTime: "2026-01-01T00:00:00Z", Logs: []*tes.TaskLog{{Metadata: map[string]string{"hostname": "worker-pod", "awsbatch_id": "batch-job"}}}}}}, nil
}
func (reconnectReader) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return &tes.Task{Id: "restart-task", State: tes.Running}, nil
}
func (reconnectReader) Close() {}

func freeDashboardPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	err = listener.Close()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestDashboardRecoversAfterFunnelServerRestart(t *testing.T) {
	httpPort, rpcPort := freeDashboardPort(t), freeDashboardPort(t)
	for rpcPort == httpPort {
		rpcPort = freeDashboardPort(t)
	}
	log := logger.NewLogger("dashboard-restart-test", logger.DefaultConfig())
	log.Discard()
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		srv := &server.Server{RPCAddress: fmt.Sprintf("127.0.0.1:%d", rpcPort), HTTPPort: strconv.Itoa(httpPort), Log: log, Tasks: &server.TaskService{Read: reconnectReader{}, Log: log, Config: config.DefaultConfig()}}
		go func() { done <- srv.Serve(ctx) }()
		return cancel, done
	}
	stop := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stop server: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	}
	cancel, done := start()
	defer func() {
		if cancel != nil {
			stop(cancel, done)
		}
	}()
	d, err := newDashboard(fmt.Sprintf("http://127.0.0.1:%d", httpPort))
	if err != nil {
		t.Fatal(err)
	}
	d.width, d.height = 120, 24
	refresh := func() {
		t.Helper()
		cmd := d.requestList(listCurrent)
		if cmd == nil {
			t.Fatal("refresh not scheduled")
		}
		d.Update(cmd())
	}
	waitForTask := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			refresh()
			if d.err == "" && len(d.tasks) == 1 && d.tasks[0].Id == "restart-task" {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("dashboard did not recover: %q", d.err)
	}
	waitForTask()
	workers, err := d.source.TaskWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].Hostname != "worker-pod" || workers[0].Metadata["awsbatch_id"] != "batch-job" {
		t.Fatalf("recorded workers from Funnel server = %+v", workers)
	}
	stop(cancel, done)
	cancel = nil
	refresh()
	if d.err == "" || !strings.Contains(d.View(), d.err) {
		t.Fatalf("dashboard did not display server outage: %q", d.View())
	}
	cancel, done = start()
	waitForTask()
	if strings.Contains(d.View(), "connection refused") {
		t.Fatalf("stale outage displayed after restart: %q", d.View())
	}
}
