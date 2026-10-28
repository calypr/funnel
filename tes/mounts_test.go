package tes

import (
	"strings"
	"testing"
)

func TestValidateMountPathsRejectsRootMounts(t *testing.T) {
	tests := []struct {
		name string
		task *Task
	}{
		{
			name: "file output parent",
			task: &Task{Outputs: []*Output{{Path: "/result.json", Url: "file:///result.json", Type: FileType_FILE}}},
		},
		{
			name: "directory output alias",
			task: &Task{Outputs: []*Output{{Path: "/tmp/..", Url: "file:///result", Type: FileType_DIRECTORY}}},
		},
		{
			name: "volume alias",
			task: &Task{Volumes: []string{"/tmp/.."}},
		},
		{
			name: "root file input parent",
			task: &Task{Inputs: []*Input{{Path: "/input.txt", Url: "file:///input.txt", Type: FileType_FILE}}},
		},
		{
			name: "root directory input",
			task: &Task{Inputs: []*Input{{Path: "/", Url: "file:///inputs", Type: FileType_DIRECTORY}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateMountPaths(test.task, "docker"); err == nil || !strings.Contains(err.Error(), "mount at /") {
				t.Fatalf("ValidateMountPaths() error = %v, want a root mount rejection", err)
			}
		})
	}
}

func TestValidateMountPathsAllowsDeclaredDockerRootStreams(t *testing.T) {
	task := &Task{
		Inputs: []*Input{{Path: "/inputs", Url: "file:///inputs", Type: FileType_DIRECTORY}},
		Outputs: []*Output{
			{Path: "/capture-out", Url: "file:///capture-out", Type: FileType_FILE},
			{Path: "/capture-err", Url: "file:///capture-err", Type: FileType_FILE},
		},
		Executors: []*Executor{
			{Stdout: "/capture-out"},
			{Stderr: "//capture-err"},
			{Workdir: "/"},
		},
	}
	if err := ValidateMountPaths(task, "docker"); err != nil {
		t.Fatalf("ValidateMountPaths() rejected declared Docker stream files: %v", err)
	}
}

func TestValidateMountPathsRejectsKubernetesRootStreamsWithWorkaround(t *testing.T) {
	task := &Task{Executors: []*Executor{{Stdout: "/arbitrary-name"}}}
	err := ValidateMountPaths(task, "k8s")
	if err == nil || !strings.Contains(err.Error(), "Kubernetes") || !strings.Contains(err.Error(), "/outputs/") {
		t.Fatalf("ValidateMountPaths() error = %v, want Kubernetes dedicated-directory guidance", err)
	}
}

func TestValidateMountPathsRejectsRootStreamConflicts(t *testing.T) {
	tests := []struct {
		name string
		task *Task
	}{
		{
			name: "input file",
			task: &Task{
				Inputs:    []*Input{{Path: "/capture/nested", Url: "file:///source", Type: FileType_FILE}},
				Executors: []*Executor{{Stdout: "/capture"}},
			},
		},
		{
			name: "directory output",
			task: &Task{
				Outputs:   []*Output{{Path: "/capture", Url: "file:///capture", Type: FileType_DIRECTORY}},
				Executors: []*Executor{{Stdout: "/capture"}},
			},
		},
		{
			name: "nested stream",
			task: &Task{Executors: []*Executor{{Stdout: "/capture", Stderr: "/capture/nested"}}},
		},
		{
			name: "workdir inside stream file",
			task: &Task{Executors: []*Executor{{Stdout: "/capture", Workdir: "/capture/subdir"}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateMountPaths(test.task, "docker"); err == nil || !strings.Contains(err.Error(), "conflicts") {
				t.Fatalf("ValidateMountPaths() error = %v, want a stream path conflict", err)
			}
		})
	}
}
