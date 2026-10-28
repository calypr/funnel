package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/assert"
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

	err = f.MapTask(task, "docker")
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

	if !assert.True(t, protoSlicesEqual(ei, f.Inputs)) {
		t.Log("Expected", fmt.Sprintf("%+v", ei))
		t.Log("Actual", fmt.Sprintf("%+v", f.Inputs))
		t.Fatal("unexpected mapper inputs")
	}

	c, err := os.ReadFile(tmp + "/inputs/testdata/contents.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != "test content\n" {
		t.Fatal("unexpected content")
	}

	if !assert.True(t, protoSlicesEqual(eo, f.Outputs)) {
		t.Log("Expected", fmt.Sprintf("%+v", eo))
		t.Log("Actual", fmt.Sprintf("%+v", f.Outputs))
		t.Fatal("unexpected mapper outputs")
	}

	for _, vol := range ev {
		fmt.Println("Expected volume:", vol)
	}

	for _, vol := range f.Volumes {
		fmt.Println("Actual volume:", vol)
	}

	if !assert.Equal(t, ev, f.Volumes) {
		t.Log("Expected", fmt.Sprintf("%+v", ev))
		t.Log("Actual", fmt.Sprintf("%+v", f.Volumes))
		t.Fatal("unexpected mapper volumes")
	}

	if f.ContainerPath(f.Outputs[0].Path) != task.Outputs[0].Path {
		t.Log("Expected", task.Outputs[0].Path)
		t.Log("Actual", f.ContainerPath(f.Outputs[0].Path))
		t.Fatal("path unmapping failed")
	}
}

func TestMapTaskRejectsRootMountsBeforeCreatingWorkDir(t *testing.T) {
	tests := []struct {
		name string
		task *tes.Task
	}{
		{
			name: "ordinary root file output",
			task: &tes.Task{Outputs: []*tes.Output{{Path: "/result.json", Url: "file:///result.json", Type: tes.FileType_FILE}}},
		},
		{
			name: "root directory output alias",
			task: &tes.Task{Outputs: []*tes.Output{{Path: "/tmp/..", Url: "file:///result", Type: tes.FileType_DIRECTORY}}},
		},
		{
			name: "root volume alias",
			task: &tes.Task{Volumes: []string{"/tmp/.."}},
		},
		{
			name: "root file input parent",
			task: &tes.Task{Inputs: []*tes.Input{{Path: "/input.txt", Url: "file:///input.txt", Type: tes.FileType_FILE}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "task")
			mapper := NewFileMapper(workDir)
			if err := mapper.MapTask(test.task, "docker"); err == nil {
				t.Fatal("MapTask() accepted a task that requires a mount at /")
			}
			if _, err := os.Stat(workDir); !os.IsNotExist(err) {
				t.Fatalf("MapTask() created work directory before rejecting root mount: stat error = %v", err)
			}
		})
	}
}

func TestMapTaskMountsDeclaredRootStreamsAsFiles(t *testing.T) {
	workDir := t.TempDir()
	mapper := NewFileMapper(workDir)
	task := &tes.Task{
		Inputs: []*tes.Input{{Path: "/inputs", Url: "file:///inputs", Type: tes.FileType_DIRECTORY}},
		Outputs: []*tes.Output{
			{Path: "/capture-out", Url: "file:///capture-out", Type: tes.FileType_FILE},
			{Path: "/capture-err", Url: "file:///capture-err", Type: tes.FileType_FILE},
		},
		Executors: []*tes.Executor{
			{Stdout: "/capture-out"},
			{Stderr: "/capture-err"},
		},
	}
	if err := mapper.MapTask(task, "docker"); err != nil {
		t.Fatalf("MapTask() error = %v", err)
	}

	want := map[string]bool{filepath.Join(workDir, "capture-out"): true, filepath.Join(workDir, "capture-err"): true}
	seenInputs := false
	for _, volume := range mapper.Volumes {
		if volume.ContainerPath == "/" {
			t.Fatal("MapTask() created a root mount for declared stream files")
		}
		if volume.ContainerPath == "/inputs" {
			seenInputs = true
		}
		if _, ok := want[volume.HostPath]; ok {
			if volume.ContainerPath != "/"+filepath.Base(volume.HostPath) {
				t.Errorf("stream volume = %+v, want exact file mount", volume)
			}
			delete(want, volume.HostPath)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing exact stream file mounts for %v, got %+v", want, mapper.Volumes)
	}
	if !seenInputs {
		t.Errorf("directory input was not kept at /inputs: %+v", mapper.Volumes)
	}
	for hostPath := range map[string]bool{
		filepath.Join(workDir, "capture-out"): true,
		filepath.Join(workDir, "capture-err"): true,
	} {
		info, err := os.Stat(hostPath)
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("precreated stream file %q: info=%v, error=%v", hostPath, info, err)
		}
	}
}

func TestMapTaskRejectsRootStreamInputConflictWithoutTruncatingInput(t *testing.T) {
	workDir := t.TempDir()
	inputPath := filepath.Join(workDir, "capture")
	if err := os.WriteFile(inputPath, []byte("preserve input"), 0600); err != nil {
		t.Fatal(err)
	}
	mapper := NewFileMapper(workDir)
	task := &tes.Task{
		Inputs:    []*tes.Input{{Path: "/capture", Url: "file:///capture", Type: tes.FileType_FILE}},
		Executors: []*tes.Executor{{Stdout: "/capture"}},
	}
	if err := mapper.MapTask(task, "docker"); err == nil {
		t.Fatal("MapTask() accepted an input and stream that share a path")
	}
	got, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "preserve input" {
		t.Fatalf("conflicting stream preparation changed input content to %q", got)
	}
}

func TestMapTaskUsesPathBoundariesForMountOverlap(t *testing.T) {
	mapper := NewFileMapper(t.TempDir())
	task := &tes.Task{Inputs: []*tes.Input{
		{Path: "/data/item", Content: "data"},
		{Path: "/database/item", Content: "database"},
	}}
	if err := mapper.MapTask(task, "docker"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, volume := range mapper.Volumes {
		seen[volume.ContainerPath] = true
	}
	if !seen["/data"] || !seen["/database"] || seen["/"] {
		t.Fatalf("path overlap merged separate mount roots: %+v", mapper.Volumes)
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

	if err := f.MapTask(task, "docker"); err != nil {
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

	if err := f.MapTask(task, "docker"); err != nil {
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

	if err := f.MapTask(task, "docker"); err != nil {
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

// assertNoNestedVolumes fails if a read-write volume lies inside another one:
// the Kubernetes backend rejects such mounts ("storage mount ... overlaps job
// mount ...").
func assertNoNestedVolumes(t *testing.T, f *FileMapper) {
	t.Helper()
	for i, a := range f.Volumes {
		for j, b := range f.Volumes {
			if i == j || a.Readonly || b.Readonly {
				continue
			}
			if f.IsSubpath(a.ContainerPath, b.ContainerPath) {
				t.Errorf("volume %q is inside volume %q; volumes: %+v", a.ContainerPath, b.ContainerPath, f.Volumes)
			}
		}
	}
}

func containerPaths(f *FileMapper) []string {
	var out []string
	for _, v := range f.Volumes {
		out = append(out, v.ContainerPath)
	}
	return out
}

// A wider volume must replace every narrower volume it covers, not just the
// first one.
func TestMapTaskWiderVolumeReplacesAllNarrowerVolumes(t *testing.T) {
	f := FileMapper{WorkDir: t.TempDir()}
	task := &tes.Task{
		Outputs: []*tes.Output{
			{Url: "file:///out/b", Path: "/out/b", Type: tes.FileType_DIRECTORY},
			{Url: "file:///out/c", Path: "/out/c", Type: tes.FileType_DIRECTORY},
			{Url: "file:///out/x.txt", Path: "/out/x.txt", Type: tes.FileType_FILE},
		},
	}

	if err := f.MapTask(task); err != nil {
		t.Fatal(err)
	}

	assertNoNestedVolumes(t, &f)
	if got := containerPaths(&f); len(got) != 1 || got[0] != "/out" {
		t.Errorf("volumes = %v, want [/out]", got)
	}
}

func TestMapTaskVolumesNeverNest(t *testing.T) {
	for name, task := range map[string]*tes.Task{
		// miniwdl's layout: command and inputs below the container dir, inputs
		// inside the work directory output, streams next to it
		"workflow engine layout": {
			Inputs: []*tes.Input{
				{Path: "/mnt/c/command", Content: "true"},
				{Path: "/mnt/c/work/_inputs/0/a.txt", Content: "a"},
				{Path: "/mnt/c/work/_inputs/1/b.txt", Content: "b"},
			},
			Outputs: []*tes.Output{
				{Url: "file:///o/work", Path: "/mnt/c/work", Type: tes.FileType_DIRECTORY},
				{Url: "file:///o/stdout.txt", Path: "/mnt/c/stdout.txt", Type: tes.FileType_FILE},
				{Url: "file:///o/stderr.txt", Path: "/mnt/c/stderr.txt", Type: tes.FileType_FILE},
			},
		},
		"inputs consolidate above outputs": {
			Inputs: []*tes.Input{
				{Path: "/data/in/a.txt", Content: "a"},
				{Path: "/data/ref/b.txt", Content: "b"},
			},
			Outputs: []*tes.Output{
				{Url: "file:///o/x", Path: "/data/out/x", Type: tes.FileType_DIRECTORY},
				{Url: "file:///o/y", Path: "/data/out/y", Type: tes.FileType_DIRECTORY},
			},
		},
		"outputs first, wider output last": {
			Outputs: []*tes.Output{
				{Url: "file:///o/1", Path: "/w/a/1", Type: tes.FileType_DIRECTORY},
				{Url: "file:///o/2", Path: "/w/b/2", Type: tes.FileType_DIRECTORY},
				{Url: "file:///o/3", Path: "/w/c/3", Type: tes.FileType_DIRECTORY},
				{Url: "file:///o/all", Path: "/w", Type: tes.FileType_DIRECTORY},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := FileMapper{WorkDir: t.TempDir()}
			if err := f.MapTask(task); err != nil {
				t.Fatal(err)
			}
			assertNoNestedVolumes(t, &f)
		})
	}
}

// "/out2" is not inside "/out": it needs its own volume.
func TestMapTaskSiblingWithSamePrefixIsMounted(t *testing.T) {
	f := FileMapper{WorkDir: t.TempDir()}
	task := &tes.Task{
		Outputs: []*tes.Output{
			{Url: "file:///o/out", Path: "/out", Type: tes.FileType_DIRECTORY},
			{Url: "file:///o/b.txt", Path: "/out2/b.txt", Type: tes.FileType_FILE},
		},
	}

	if err := f.MapTask(task); err != nil {
		t.Fatal(err)
	}

	got := containerPaths(&f)
	if len(got) != 2 || got[0] != "/out" || got[1] != "/out2" {
		t.Errorf("volumes = %v, want [/out /out2]", got)
	}
}

func TestIsSubpathComparesWholeComponents(t *testing.T) {
	f := FileMapper{}
	for _, tc := range []struct {
		p, base string
		want    bool
	}{
		{"/data", "/data", true},
		{"/data/x", "/data", true},
		{"/data/x", "/data/", true},
		{"/data2", "/data", false},
		{"/data2/x", "/data", false},
		{"/dat", "/data", false},
		{"/anything", "/", true},
		{"/data/../etc", "/data", false},
	} {
		if got := f.IsSubpath(tc.p, tc.base); got != tc.want {
			t.Errorf("IsSubpath(%q, %q) = %v, want %v", tc.p, tc.base, got, tc.want)
		}
	}
}

// A task path can't reach a sibling of the work directory whose name starts
// with the work directory's name.
func TestHostPathRejectsSiblingOfWorkDir(t *testing.T) {
	root := t.TempDir()
	f := FileMapper{WorkDir: filepath.Join(root, "task")}

	if p, err := f.HostPath("/../task2/x"); err == nil {
		t.Errorf("HostPath accepted %q", p)
	}
	if _, err := f.HostPath("/data/x"); err != nil {
		t.Errorf("HostPath(/data/x): %v", err)
	}
}
