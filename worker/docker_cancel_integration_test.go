package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func TestDockerCancellationStopsContainer(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}
	name := fmt.Sprintf("funnel-cancel-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such container") {
			t.Errorf("cleaning up container: %v: %s", err, out)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	builder := &events.TaskBuilder{Task: &tes.Task{}}
	event := events.NewExecutorWriter("test", 0, 0, builder)
	conf := config.DefaultConfig().Worker
	command := &DockerCommand{Name: name, DriverCommand: "docker", PullCommand: "image inspect {{.Image}}", RunCommand: conf.Container.RunCommand, StopCommand: conf.Container.StopCommand, RemoveContainer: true, Event: event, Command: Command{Image: "alpine:latest", ShellCommand: []string{"sleep", "60"}}}
	step := &stepWorker{Conf: conf, Command: command, Event: event}
	done := make(chan error, 1)
	go func() { done <- step.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		output, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", name).Output()
		if err == nil && strings.TrimSpace(string(output)) == "true" {
			started = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("worker exited before container started: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !started {
		t.Fatal("container did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("worker error = %v, want cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
	if code := builder.Task.GetLogs()[0].GetLogs()[0].GetExitCode(); code != 137 {
		t.Fatalf("exit code = %d, want 137 after force removal", code)
	}
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such object") && !strings.Contains(string(out), "No such container") {
		t.Fatalf("inspect container: %v: %s", err, out)
	}
	if err == nil && strings.TrimSpace(string(out)) != "false" {
		t.Fatalf("container remains running after cancellation: %s", out)
	}
}

func TestDockerCancellationWithSlowCleanup(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}
	name := fmt.Sprintf("funnel-slow-stop-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such container") {
			t.Errorf("cleaning up container: %v: %s", err, out)
		}
	})
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "alpine:latest", "sleep", "60").CombinedOutput(); err != nil {
		t.Fatalf("starting container: %v: %s", err, out)
	}
	wrapper := filepath.Join(t.TempDir(), "docker-delayed")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nsleep 6\nexec docker \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := DockerCommand{
		Name: name, DriverCommand: wrapper, StopCommand: "rm -f {{.Name}}",
		Event: events.NewExecutorWriter("test", 0, 0, events.Noop{}),
	}
	if err := command.Stop(); err != nil {
		t.Fatalf("stopping container with delayed Docker command: %v", err)
	}
	out, err := exec.Command("docker", "inspect", name).CombinedOutput()
	if err == nil || (!strings.Contains(string(out), "No such object") && !strings.Contains(string(out), "No such container")) {
		t.Fatalf("container was not removed: %v: %s", err, out)
	}
}
