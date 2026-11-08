package compute

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/taskmodel"
	"github.com/calypr/funnel/tes"
)

type hpcBindingReader struct {
	taskmodel.ReadOnlyServer
	task *tes.Task
}

func (r hpcBindingReader) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return r.task, nil
}

func hpcCancelRecorder(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	driver := filepath.Join(dir, "cancel")
	t.Setenv("FUNNEL_HPC_TEST_CAPTURE", capture)
	if err := os.WriteFile(driver, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FUNNEL_HPC_TEST_CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return driver, capture
}

func TestHPCCancelRejectsTaskMetadataAsAuthority(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = t.TempDir()
	conf.HPCStateDir = filepath.Join(t.TempDir(), "controller")
	conf.LocalStorage.Disabled = true
	driver, capture := hpcCancelRecorder(t)
	task := &tes.Task{Id: "owned-task", Logs: []*tes.TaskLog{{Metadata: map[string]string{"slurm_id": "424242"}}}}
	backend := &HPCBackend{Name: "slurm", Conf: conf, CancelCmd: driver, Database: hpcBindingReader{task: task}}
	if err := backend.Cancel(t.Context(), task.Id); err == nil {
		t.Fatal("cancellation trusted task-editable provider metadata")
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("cancel command dispatched without trusted submission binding: %v", err)
	}
}

func TestHPCCancelUsesPersistedSubmissionBinding(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = t.TempDir()
	conf.HPCStateDir = filepath.Join(t.TempDir(), "controller")
	conf.LocalStorage.Disabled = true
	driver, capture := hpcCancelRecorder(t)
	submit := filepath.Join(t.TempDir(), "submit")
	if err := os.WriteFile(submit, []byte("#!/bin/sh\nprintf '101\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "owned-task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}, Logs: []*tes.TaskLog{{Metadata: map[string]string{"slurm_id": "424242"}}}}
	backend := &HPCBackend{Name: "slurm", Conf: conf, SubmitCmd: submit, Template: "task {{.TaskId}}", Event: events.Noop{}, ExtractID: strings.TrimSpace}
	if err := backend.Submit(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	// A fresh controller instance must not recover authority from mutable logs.
	restarted := &HPCBackend{Name: "slurm", Conf: conf, CancelCmd: driver, Database: hpcBindingReader{task: task}}
	if err := restarted.Cancel(t.Context(), task.Id); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "101" {
		t.Fatalf("cancel selected editable metadata instead of submitted job: %q", raw)
	}
}

func TestHPCBindingsRejectTaskAccessibleState(t *testing.T) {
	for _, location := range []string{"workdir", "storage", "missing"} {
		t.Run(location, func(t *testing.T) {
			conf := config.DefaultConfig()
			conf.Worker.WorkDir = t.TempDir()
			conf.HPCStateDir = filepath.Join(t.TempDir(), "controller")
			conf.LocalStorage.Disabled = true
			switch location {
			case "workdir":
				conf.HPCStateDir = filepath.Join(conf.Worker.WorkDir, "private")
			case "storage":
				conf.LocalStorage.Disabled = false
				conf.LocalStorage.AllowedDirs = []string{conf.HPCStateDir}
			case "missing":
				conf.HPCStateDir = ""
			}
			b := &HPCBackend{Name: "slurm", Conf: conf}
			if err := b.saveJobBinding("owned-task", "101"); err == nil {
				t.Fatal("accepted task-accessible or unspecified controller state")
			}
		})
	}
}
