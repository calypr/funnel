package taskmodel

import (
	"strings"
	"testing"

	"github.com/calypr/funnel/tes"
)

func TestValidateMountPathsRejectsRootMounts(t *testing.T) {
	tests := []struct {
		name string
		task *tes.Task
	}{
		{
			name: "file output parent",
			task: &tes.Task{Outputs: []*tes.Output{{Path: "/result.json", Url: "file:///result.json", Type: tes.FileType_FILE.Enum()}}},
		},
		{
			name: "directory output alias",
			task: &tes.Task{Outputs: []*tes.Output{{Path: "/tmp/..", Url: "file:///result", Type: tes.FileType_DIRECTORY.Enum()}}},
		},
		{
			name: "volume alias",
			task: &tes.Task{Volumes: []string{"/tmp/.."}},
		},
		{
			name: "root file input parent",
			task: &tes.Task{Inputs: []*tes.Input{{Path: "/input.txt", Url: "file:///input.txt", Type: tes.FileType_FILE}}},
		},
		{
			name: "root directory input",
			task: &tes.Task{Inputs: []*tes.Input{{Path: "/", Url: "file:///inputs", Type: tes.FileType_DIRECTORY}}},
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
	task := &tes.Task{
		Inputs: []*tes.Input{{Path: "/inputs", Url: "file:///inputs", Type: tes.FileType_DIRECTORY}},
		Outputs: []*tes.Output{
			{Path: "/capture-out", Url: "file:///capture-out", Type: tes.FileType_FILE.Enum()},
			{Path: "/capture-err", Url: "file:///capture-err", Type: tes.FileType_FILE.Enum()},
		},
		Executors: []*tes.Executor{
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
	task := &tes.Task{Executors: []*tes.Executor{{Stdout: "/arbitrary-name"}}}
	err := ValidateMountPaths(task, "k8s")
	if err == nil || !strings.Contains(err.Error(), "Kubernetes") || !strings.Contains(err.Error(), "/outputs/") {
		t.Fatalf("ValidateMountPaths() error = %v, want Kubernetes dedicated-directory guidance", err)
	}
}

func TestValidateMountPathsRejectsRootStreamConflicts(t *testing.T) {
	tests := []struct {
		name string
		task *tes.Task
	}{
		{
			name: "input file",
			task: &tes.Task{
				Inputs:    []*tes.Input{{Path: "/capture/nested", Url: "file:///source", Type: tes.FileType_FILE}},
				Executors: []*tes.Executor{{Stdout: "/capture"}},
			},
		},
		{
			name: "directory output",
			task: &tes.Task{
				Outputs:   []*tes.Output{{Path: "/capture", Url: "file:///capture", Type: tes.FileType_DIRECTORY.Enum()}},
				Executors: []*tes.Executor{{Stdout: "/capture"}},
			},
		},
		{
			name: "nested stream",
			task: &tes.Task{Executors: []*tes.Executor{{Stdout: "/capture", Stderr: "/capture/nested"}}},
		},
		{
			name: "workdir inside stream file",
			task: &tes.Task{Executors: []*tes.Executor{{Stdout: "/capture", Workdir: "/capture/subdir"}}},
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
