package schedulerprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	processGateEnv = "FUNNEL_TEST_SCHEDULER_PROCESS_INTEGRATION"
	backendEnv     = "FUNNEL_SCHEDULER_TEST_BACKEND"
	processModeEnv = "FUNNEL_SCHEDULER_PROCESS_HELPER"
)

func TestSchedulerProcessHelper(t *testing.T) {
	mode := os.Getenv(processModeEnv)
	if mode == "" {
		return
	}
	settings := dbSettingsFromEnv(t)
	conf := processConfig(t, settings)
	log := logger.NewLogger("scheduler-process-helper", logger.DefaultConfig())

	switch mode {
	case "server":
		runServerHelper(t, conf, log)
	case "node":
		runNodeHelper(t, conf, log)
	case "scheduler-commit-barrier":
		runSchedulerCommitBarrier(t, conf, settings, log)
	case "worker-admission-barrier":
		runWorkerAdmissionBarrier(t, conf, log)
	case "worker-observer":
		runWorkerObserver(t, conf, log)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func processConfig(t *testing.T, settings dbSettings) *config.Config {
	t.Helper()
	conf := baseProcessConfig(settings)
	conf.Server.HostName = "127.0.0.1"
	conf.Server.HTTPPort = envOr(t, "FUNNEL_PROCESS_HTTP_PORT", "18080")
	conf.Server.RPCPort = envOr(t, "FUNNEL_PROCESS_RPC_PORT", "19090")
	conf.RPCClient.ServerAddress = conf.Server.RPCAddress()
	conf.Compute = "manual"
	conf.Scheduler.ScheduleRate = durationpb.New(30 * time.Millisecond)
	conf.Scheduler.ScheduleChunk = 100
	conf.Scheduler.NodePingTimeout = timeout(5 * time.Second)
	conf.Scheduler.NodeInitTimeout = timeout(time.Minute)
	conf.Scheduler.NodeDeadTimeout = timeout(time.Minute)
	conf.Node.UpdateRate = durationpb.New(40 * time.Millisecond)
	conf.Node.Timeout = timeout(15 * time.Minute)
	conf.Node.Resources = &config.Resources{Cpus: 1, RamGb: 2, DiskGb: 4}
	conf.Worker.PollingRate = durationpb.New(25 * time.Millisecond)
	conf.Worker.LogUpdateRate = durationpb.New(25 * time.Millisecond)
	conf.Worker.LeaveWorkDir = true
	conf.Worker.WorkDir = envOr(t, "FUNNEL_PROCESS_WORK_DIR", filepath.Join(t.TempDir(), "worker"))
	conf.Worker.WorkDir = envOr(t, "FUNNEL_PROCESS_NODE_WORK_DIR", conf.Worker.WorkDir)
	conf.Node.ID = os.Getenv("FUNNEL_PROCESS_NODE_ID")
	markers := os.Getenv("FUNNEL_PROCESS_MARKER_DIR")
	if markers != "" {
		configureMarkerVolume(conf, markers)
	}
	return conf
}

func baseProcessConfig(settings dbSettings) *config.Config {
	conf := config.DefaultConfig()
	conf.Database = settings.backend
	if settings.backend == "postgres" {
		conf.Postgres.Host = settings.host
		conf.Postgres.Database = settings.name
		conf.Postgres.User = settings.user
		conf.Postgres.Password = settings.pass
		conf.Postgres.AdminUser = settings.user
		conf.Postgres.AdminPassword = settings.pass
	} else {
		conf.MongoDB.Addrs = []string{settings.uri}
		conf.MongoDB.Database = settings.name
	}
	return conf
}

func configureMarkerVolume(conf *config.Config, markers string) {
	quotedPath := "'" + strings.ReplaceAll(markers, "'", "'\\''") + "'"
	conf.Worker.Container.RunCommand = strings.Replace(
		conf.Worker.Container.RunCommand,
		"{{.Image}}",
		"--volume "+quotedPath+":/funnel-process-test {{.Image}}",
		1,
	)
}

type dockerContainerCleanup struct {
	t     *testing.T
	names []string
}

func newDockerContainerCleanup(t *testing.T) *dockerContainerCleanup {
	t.Helper()
	cleanup := &dockerContainerCleanup{t: t}
	t.Cleanup(cleanup.remove)
	return cleanup
}

func (c *dockerContainerCleanup) add(name string) {
	c.names = append(c.names, name)
}

func (c *dockerContainerCleanup) remove() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range c.names {
		output, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
		if err == nil || strings.Contains(string(output), "No such container") {
			continue
		}
		c.t.Errorf("remove test container %s: %v\n%s", name, err, output)
	}
}

func timeout(d time.Duration) *config.TimeoutConfig {
	if d == 0 {
		return &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Disabled{Disabled: true}}
	}
	return &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(d)}}
}

func waitForServer(t *testing.T, process *childProcess, port string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.exitError(); err != nil {
			t.Fatalf("server process exited before accepting connections: %v\n%s", err, process.diagnostics())
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 100*time.Millisecond)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close readiness probe connection: %v", closeErr)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server on port %s did not become ready\n%s", port, process.diagnostics())
}

func waitForNode(t *testing.T, store schedulerStore, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		node, err := store.GetNode(systemContext(), &scheduler.GetNodeRequest{Id: id})
		if err == nil && node.State == scheduler.NodeState_ALIVE {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("node %s did not become ALIVE", id)
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release local port: %v", err)
	}
	return fmt.Sprint(port)
}

func processFailure(processes ...*childProcess) string {
	var diagnostics []string
	for _, process := range processes {
		if err := process.exitError(); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("process exited: %v\n%s", err, process.diagnostics()))
		}
	}
	return strings.Join(diagnostics, "\n")
}

type childProcess struct {
	cmd     *exec.Cmd
	lines   chan string
	done    chan struct{}
	mu      sync.Mutex
	stdout  strings.Builder
	stderr  strings.Builder
	waitErr error
	pending []byte
}

func startHelper(t *testing.T, settings dbSettings, mode string, extra map[string]string) *childProcess {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestSchedulerProcessHelper$")
	command.Env = append(os.Environ(), settings.env()...)
	command.Env = append(command.Env, processModeEnv+"="+mode)
	for key, value := range extra {
		command.Env = append(command.Env, key+"="+value)
	}
	process := &childProcess{cmd: command, lines: make(chan string, 128), done: make(chan struct{})}
	command.Stdout = process
	command.Stderr = childStderr{process: process}
	if err := command.Start(); err != nil {
		t.Fatalf("start %s helper: %v", mode, err)
	}
	go func() {
		err := command.Wait()
		process.mu.Lock()
		process.waitErr = err
		process.mu.Unlock()
		close(process.done)
	}()
	t.Cleanup(func() {
		if process.exitError() == nil {
			if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill child process %s during cleanup: %v", mode, err)
			}
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				t.Errorf("child process %s did not stop during cleanup", mode)
			}
		}
		if t.Failed() {
			t.Logf("child process %s diagnostics\n%s", mode, process.diagnostics())
		}
	})
	return process
}

func (p *childProcess) Write(data []byte) (int, error) {
	p.mu.Lock()
	p.stdout.Write(data)
	p.pending = append(p.pending, data...)
	for {
		newline := bytes.IndexByte(p.pending, '\n')
		if newline < 0 {
			break
		}
		line := strings.TrimSuffix(string(p.pending[:newline]), "\r")
		p.pending = p.pending[newline+1:]
		if strings.HasPrefix(line, "PROCESS_EVENT:") {
			select {
			case p.lines <- line:
			default:
			}
		}
	}
	p.mu.Unlock()
	return len(data), nil
}

type childStderr struct {
	process *childProcess
}

func (w childStderr) Write(data []byte) (int, error) {
	w.process.mu.Lock()
	defer w.process.mu.Unlock()
	return w.process.stderr.Write(data)
}

func (p *childProcess) waitForLine(t *testing.T, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case line := <-p.lines:
			if strings.Contains(line, want) {
				return line
			}
		case <-p.done:
			t.Fatalf("process exited before %q: %v\n%s", want, p.exitError(), p.diagnostics())
		case <-deadline.C:
			t.Fatalf("process did not emit %q within %s\n%s", want, timeout, p.diagnostics())
		}
	}
}

func (p *childProcess) exitError() error {
	select {
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.waitErr
	default:
		return nil
	}
}

func (p *childProcess) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case <-p.done:
		return p.exitError()
	case <-time.After(timeout):
		return fmt.Errorf("process did not exit within %s", timeout)
	}
}

func (p *childProcess) diagnostics() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return "stdout:\n" + p.stdout.String() + "\nstderr:\n" + p.stderr.String()
}

func stopByKill(t *testing.T, process *childProcess) {
	t.Helper()
	if err := process.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
		t.Fatalf("kill process: %v", err)
	}
	if err := process.wait(t, 10*time.Second); err == nil {
		t.Fatal("killed process exited successfully")
	}
}

func stopChildren(t *testing.T, processes []*childProcess) {
	t.Helper()
	for _, process := range processes {
		if process.exitError() != nil {
			continue
		}
		if err := process.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			if killErr := process.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				t.Errorf("kill child process after failed SIGTERM: %v", killErr)
			}
			if waitErr := process.wait(t, 5*time.Second); waitErr != nil {
				t.Errorf("child process did not stop after failed SIGTERM: %v\n%s", waitErr, process.diagnostics())
			}
			continue
		}
		if err := process.wait(t, 5*time.Second); err != nil {
			if killErr := process.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				t.Errorf("kill child process after SIGTERM timeout: %v", killErr)
			}
			if killErr := process.wait(t, 5*time.Second); killErr != nil {
				t.Errorf("stop child process: %v\n%s", err, process.diagnostics())
			}
		}
	}
}

func envOr(t *testing.T, key, fallback string) string {
	t.Helper()
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("required helper environment variable %s is empty", key)
	}
	return value
}
