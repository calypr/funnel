package worker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calypr/funnel/api/tes"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/storage"
	"github.com/calypr/funnel/internal/worker/workspace"
)

func TestProcessOutputsRejectsOutsideWorkspaceSymlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside marker"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	conf := appconfig.DefaultConfig()
	store, err := storage.NewHTTP(conf.HTTPStorage)
	if err != nil {
		t.Fatal(err)
	}
	mapper := workspace.NewFileMapper(t.TempDir())
	task := &tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}, Outputs: []*tes.Output{{Path: "/outputs/result", Url: endpoint.URL + "/result", Type: tes.File.Enum()}}}
	if err := mapper.MapTask(task, "docker"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, mapper.Outputs[0].Path); err != nil {
		t.Fatal(err)
	}
	run := &helper{}
	(&DefaultWorker{Conf: conf.Worker, Store: store}).processOutputs(t.Context(), events.NewTaskWriter(task.Id, 0, events.Noop{}), mapper, run)
	if run.syserr == nil {
		t.Error("outside-workspace output was accepted")
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("outside-workspace output reached uploader: %d requests", got)
	}
}

type replacedOutputStore struct {
	storage.Storage
	source, outside string
	got             []byte
}

func (s *replacedOutputStore) Put(_ context.Context, url, path string) (*storage.Object, error) {
	if err := os.Remove(s.source); err != nil {
		return nil, err
	}
	if err := os.Symlink(s.outside, s.source); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	s.got = data
	return &storage.Object{URL: url, Size: int64(len(data))}, err
}

func TestProcessOutputsSourceReplacementCannotChangePublishedBytes(t *testing.T) {
	mapper := workspace.NewFileMapper(t.TempDir())
	task := &tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}, Outputs: []*tes.Output{{Path: "/outputs/result", Url: "test://result", Type: tes.File.Enum()}}}
	if err := mapper.MapTask(task, "docker"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapper.Outputs[0].Path, []byte("task output"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside marker"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &replacedOutputStore{source: mapper.Outputs[0].Path, outside: outside}
	run := &helper{}
	(&DefaultWorker{Conf: appconfig.DefaultConfig().Worker, Store: store}).processOutputs(t.Context(), events.NewTaskWriter(task.Id, 0, events.Noop{}), mapper, run)
	if run.syserr != nil {
		t.Fatal(run.syserr)
	}
	if string(store.got) != "task output" {
		t.Fatalf("published bytes changed after source replacement: %q", store.got)
	}
}

func TestProcessOutputsPreservesConfinedLinksAndScratch(t *testing.T) {
	for _, kind := range []string{"relative", "container-absolute", "host-absolute", "directory", "wildcard", "scratch"} {
		t.Run(kind, func(t *testing.T) {
			mapper := workspace.NewFileMapper(t.TempDir())
			path := "/outputs/result.txt"
			typeValue := tes.File
			if kind == "directory" {
				path = "/outputs/link"
				typeValue = tes.Directory
			}
			if kind == "wildcard" {
				path = "/outputs/*.txt"
			}
			task := &tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}, Outputs: []*tes.Output{{Path: path, Url: "test://result", Type: typeValue.Enum()}}}
			if err := mapper.MapTask(task, "docker"); err != nil {
				t.Fatal(err)
			}
			base := mapper.WorkDir
			conf := appconfig.DefaultConfig().Worker
			conf.MaxParallelTransfers = 1
			if kind == "scratch" {
				mapper.ScratchDir = t.TempDir()
				base = mapper.ScratchDir
				conf.ScratchPath = base
			}
			if err := os.MkdirAll(filepath.Join(base, "data"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(base, "outputs"), 0700); err != nil {
				t.Fatal(err)
			}
			actual := filepath.Join(base, "data", "source.txt")
			if err := os.WriteFile(actual, []byte("allowed output"), 0750); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(base, "outputs", "result.txt")
			target := "../data/source.txt"
			switch kind {
			case "container-absolute":
				target = "/data/source.txt"
			case "host-absolute":
				target = actual
			case "directory":
				link = filepath.Join(base, "outputs", "link")
				target = "../data"
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			store := &capturingUploadStorage{uploaded: make(map[string]string)}
			writer := &publicationEvents{}
			run := &helper{}
			(&DefaultWorker{Conf: conf, Store: store}).processOutputs(t.Context(), events.NewTaskWriter(task.Id, 0, writer), mapper, run)
			if run.syserr != nil {
				t.Fatal(run.syserr)
			}
			if len(store.uploaded) != 1 {
				t.Fatalf("uploaded=%v", store.uploaded)
			}
			for _, content := range store.uploaded {
				if content != "allowed output" {
					t.Fatalf("contents=%q", content)
				}
			}
			for _, event := range writer.snapshot() {
				if event.Type == events.Type_TASK_OUTPUTS {
					for _, file := range event.GetOutputs().Value {
						if strings.Contains(file.Path, ".funnel-outputs-") {
							t.Fatalf("private path leaked into output log: %s", file.Path)
						}
					}
				}
			}
		})
	}
}
