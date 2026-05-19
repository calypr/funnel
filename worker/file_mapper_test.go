package worker

import (
	"fmt"
	"os"
	"testing"

	"github.com/go-test/deep"
	"github.com/ohsu-comp-bio/funnel/tes"
)

func TestMapTask(t *testing.T) {
	tmp, err := os.MkdirTemp("", "funnel-test-mapper")
	if err != nil {
		t.Fatal(err)
	}
	f := FileMapper{
		WorkDir: tmp,
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	task := &tes.Task{
		Inputs: []*tes.Input{
			{
				Name: "f1",
				Url:  "file://" + cwd + "/testdata/f1.txt",
				Path: "/inputs/testdata/f1.txt",
			},
			{
				Name: "f4",
				Url:  "file://" + cwd + "/testdata/f4",
				Path: "/inputs/testdata/f4",
				Type: tes.FileType_DIRECTORY,
			},
			{
				Name:    "c1",
				Path:    "/inputs/testdata/contents.txt",
				Content: "test content\n",
			},
		},
		Outputs: []*tes.Output{
			{
				Name: "stdout-0",
				Url:  "file://" + cwd + "/testdata/stdout-first",
				Path: "/outputs/stdout-0",
			},
			{
				Name: "o9",
				Url:  "file://" + cwd + "/testdata/o9",
				Path: "/outputs/sub/o9",
				Type: tes.FileType_DIRECTORY,
			},
		},
		Volumes: []string{"/volone", "/voltwo"},
	}

	err = f.MapTask(task)
	if err != nil {
		t.Fatal(err)
	}

	ei := []*tes.Input{
		{
			Name: "f1",
			Url:  "file://" + cwd + "/testdata/f1.txt",
			Path: tmp + "/inputs/testdata/f1.txt",
		},
		{
			Name: "f4",
			Url:  "file://" + cwd + "/testdata/f4",
			Path: tmp + "/inputs/testdata/f4",
			Type: tes.FileType_DIRECTORY,
		},
	}

	eo := []*tes.Output{
		{
			Name: "stdout-0",
			Url:  "file://" + cwd + "/testdata/stdout-first",
			Path: tmp + "/outputs/stdout-0",
		},
		{
			Name: "o9",
			Url:  "file://" + cwd + "/testdata/o9",
			Path: tmp + "/outputs/sub/o9",
			Type: tes.FileType_DIRECTORY,
		},
	}

	// consolidateVolumes() collapses the three input files under
	// /inputs/testdata into one read-write ancestor directory mount.
	ev := []Volume{
		{
			HostPath:      tmp + "/volone",
			ContainerPath: "/volone",
			Readonly:      false,
		},
		{
			HostPath:      tmp + "/voltwo",
			ContainerPath: "/voltwo",
			Readonly:      false,
		},
		{
			HostPath:      tmp + "/tmp",
			ContainerPath: "/tmp",
			Readonly:      false,
		},
		{
			HostPath:      tmp + "/outputs",
			ContainerPath: "/outputs",
			Readonly:      false,
		},
		{
			HostPath:      tmp + "/inputs/testdata",
			ContainerPath: "/inputs/testdata",
			Readonly:      false,
		},
	}

	// InputVolumes tracks each individual input volume before consolidation.
	eiv := []Volume{
		{
			HostPath:      tmp + "/inputs/testdata/f1.txt",
			ContainerPath: "/inputs/testdata/f1.txt",
		},
		{
			HostPath:      tmp + "/inputs/testdata/f4",
			ContainerPath: "/inputs/testdata/f4",
		},
		{
			HostPath:      tmp + "/inputs/testdata/contents.txt",
			ContainerPath: "/inputs/testdata/contents.txt",
		},
	}

	if diff := deep.Equal(f.Inputs, ei); diff != nil {
		t.Log("Expected", fmt.Sprintf("%+v", ei))
		t.Log("Actual", fmt.Sprintf("%+v", f.Inputs))
		for _, d := range diff {
			t.Log("Diff", d)
		}
		t.Fatal("unexpected mapper inputs")
	}

	c, err := os.ReadFile(tmp + "/inputs/testdata/contents.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != "test content\n" {
		t.Fatal("unexpected content")
	}

	if diff := deep.Equal(f.Outputs, eo); diff != nil {
		t.Log("Expected", fmt.Sprintf("%+v", eo))
		t.Log("Actual", fmt.Sprintf("%+v", f.Outputs))
		for _, d := range diff {
			t.Log("Diff", d)
		}
		t.Fatal("unexpected mapper outputs")
	}

	if diff := deep.Equal(f.Volumes, ev); diff != nil {
		t.Log("Expected", fmt.Sprintf("%+v", ev))
		t.Log("Actual", fmt.Sprintf("%+v", f.Volumes))
		for _, d := range diff {
			t.Log("Diff", d)
		}
		t.Fatal("unexpected mapper volumes")
	}

	if diff := deep.Equal(f.InputVolumes, eiv); diff != nil {
		t.Log("Expected", fmt.Sprintf("%+v", eiv))
		t.Log("Actual", fmt.Sprintf("%+v", f.InputVolumes))
		for _, d := range diff {
			t.Log("Diff", d)
		}
		t.Fatal("unexpected mapper input volumes")
	}

	if f.ContainerPath(f.Outputs[0].Path) != task.Outputs[0].Path {
		t.Log("Expected", task.Outputs[0].Path)
		t.Log("Actual", f.ContainerPath(f.Outputs[0].Path))
		t.Fatal("path unmapping failed")
	}
}

// TestMapTaskInputOutputSameDirNoDuplicateVolume reproduces the bug where a
// task with both an input and an output that resolve to the same container
// directory produced a duplicate volume mount (invalid in Kubernetes).
func TestMapTaskInputOutputSameDirNoDuplicateVolume(t *testing.T) {
	tmp, err := os.MkdirTemp("", "funnel-test-mapper-dedup")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	f := FileMapper{WorkDir: tmp}

	task := &tes.Task{
		Inputs: []*tes.Input{
			{
				Name:    "in1",
				Path:    "/data/input.txt",
				Content: "hello",
			},
		},
		Outputs: []*tes.Output{
			{
				Name: "out1",
				Url:  "file:///out/output.txt",
				Path: "/data/output.txt",
				Type: tes.FileType_FILE,
			},
		},
	}

	if err := f.MapTask(task); err != nil {
		t.Fatal(err)
	}

	// Verify no two volumes share the same ContainerPath (the K8s duplicate mount error).
	seen := map[string]int{}
	for _, v := range f.Volumes {
		seen[v.ContainerPath]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Errorf("duplicate ContainerPath %q appears %d times in Volumes (causes K8s invalid mountPath)", path, count)
		}
	}

	// /data should appear exactly once and cover both the input and the output.
	if seen["/data"] != 1 {
		t.Errorf("expected /data to appear exactly once in Volumes, got %d; volumes: %+v", seen["/data"], f.Volumes)
	}
}

// TestMapTaskInputOutputOverlapNoDuplicateVolume tests the variant where the
// output directory is a parent of the input directory.
func TestMapTaskInputOutputOverlapNoDuplicateVolume(t *testing.T) {
	tmp, err := os.MkdirTemp("", "funnel-test-mapper-overlap")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	f := FileMapper{WorkDir: tmp}

	// Input under /data/inputs/, output dir is /data (parent of input ancestor).
	task := &tes.Task{
		Inputs: []*tes.Input{
			{
				Name:    "in1",
				Path:    "/data/inputs/file.txt",
				Content: "hello",
			},
		},
		Outputs: []*tes.Output{
			{
				Name: "out1",
				Url:  "file:///out/result",
				Path: "/data/result",
				Type: tes.FileType_DIRECTORY,
			},
		},
	}

	if err := f.MapTask(task); err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	for _, v := range f.Volumes {
		seen[v.ContainerPath]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Errorf("duplicate ContainerPath %q appears %d times in Volumes", path, count)
		}
	}
}

// TestMapTaskMultipleInputsAndOutputs mirrors the concrete failure case from
// the bug report: one input and one output with a /tmp volume also present.
func TestMapTaskMultipleInputsAndOutputs(t *testing.T) {
	tmp, err := os.MkdirTemp("", "funnel-test-mapper-multi")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	f := FileMapper{WorkDir: tmp}

	task := &tes.Task{
		Inputs: []*tes.Input{
			{Name: "a", Path: "/data/a.txt", Content: "a"},
			{Name: "b", Path: "/data/b.txt", Content: "b"},
		},
		Outputs: []*tes.Output{
			{Name: "out", Url: "file:///out/c.txt", Path: "/data/c.txt", Type: tes.FileType_FILE},
		},
	}

	if err := f.MapTask(task); err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	for _, v := range f.Volumes {
		seen[v.ContainerPath]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Errorf("duplicate ContainerPath %q appears %d times in Volumes", path, count)
		}
	}

	if seen["/data"] != 1 {
		t.Errorf("expected /data once in Volumes, got %d; volumes: %+v", seen["/data"], f.Volumes)
	}
}
