package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/worker/workspace"
)

func TestProcessOutputsPublishesOverlappingReadOnlyFiles(t *testing.T) {
	file := &tes.Output{Path: "/outputs/results/log.txt", Url: "test://file", Type: tes.File.Enum()}
	directory := &tes.Output{Path: "/outputs/results", Url: "test://directory", Type: tes.Directory.Enum()}
	wildcard := &tes.Output{Path: "/outputs/results/*.txt", PathPrefix: "/outputs/results", Url: "test://wildcard", Type: tes.File.Enum()}
	for _, tc := range []struct {
		name    string
		outputs []*tes.Output
		urls    []string
	}{
		{name: "duplicate file", outputs: []*tes.Output{file, {Path: file.Path, Url: "test://duplicate", Type: tes.File.Enum()}}, urls: []string{"test://file", "test://duplicate"}},
		{name: "directory then file", outputs: []*tes.Output{directory, file}, urls: []string{"test://directory/log.txt", "test://file"}},
		{name: "file then directory", outputs: []*tes.Output{file, directory}, urls: []string{"test://file", "test://directory/log.txt"}},
		{name: "wildcard then file", outputs: []*tes.Output{wildcard, file}, urls: []string{"test://wildcard/log.txt", "test://file"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapper := workspace.NewFileMapper(t.TempDir())
			if err := mapper.MapTask(&tes.Task{Outputs: tc.outputs}, "docker"); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(mapper.WorkDir, "outputs", "results", "log.txt")
			if err := os.WriteFile(source, []byte("read-only diagnostic"), 0444); err != nil {
				t.Fatal(err)
			}
			store := &capturingUploadStorage{uploaded: make(map[string]string)}
			run := &helper{}
			worker := &DefaultWorker{Conf: &config.Worker{MaxParallelTransfers: 1}, Store: store}
			worker.processOutputs(t.Context(), events.NewTaskWriter("task", 0, events.Noop{}), mapper, run)
			if run.syserr != nil {
				t.Fatalf("valid overlapping outputs failed: %v", run.syserr)
			}
			if len(store.uploaded) != len(tc.urls) {
				t.Fatalf("published %v, want URLs %v", store.uploaded, tc.urls)
			}
			for _, url := range tc.urls {
				if store.uploaded[url] != "read-only diagnostic" {
					t.Fatalf("output %s = %q", url, store.uploaded[url])
				}
			}
		})
	}
}
