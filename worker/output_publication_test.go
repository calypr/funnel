package worker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
	"google.golang.org/protobuf/types/known/durationpb"
)

type publicationEvents struct {
	mu    sync.Mutex
	items []*events.Event
}

func (w *publicationEvents) WriteEvent(_ context.Context, ev *events.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = append(w.items, ev)
	return nil
}
func (*publicationEvents) Close() {}
func (w *publicationEvents) snapshot() []*events.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*events.Event(nil), w.items...)
}
func (w *publicationEvents) count(message string) int {
	count := 0
	for _, item := range w.snapshot() {
		if item.GetSystemLog().GetMsg() == message {
			count++
		}
	}
	return count
}

func workerPublicationS3(t *testing.T, handler http.HandlerFunc) *storage.GenericS3 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := storage.NewGenericS3(&config.GenericS3Storage{Endpoint: server.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func workerOutputFile(t *testing.T, name, content string) *tes.Output {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return &tes.Output{Url: "s3://outputs/" + name, Path: path, Type: tes.File.Enum()}
}

func TestUploadOutputsKeepsAcknowledgedFilesOnPartialFailure(t *testing.T) {
	var reads atomic.Int32
	store := workerPublicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			reads.Add(1)
			w.Header().Set("ETag", `"stale-output"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "failed.txt") {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>upload denied</Message></Error>`)
			return
		}
		w.Header().Set("ETag", `"new-write"`)
		w.WriteHeader(http.StatusOK)
	})
	outputs := []*tes.Output{workerOutputFile(t, "good.txt", "confirmed"), workerOutputFile(t, "failed.txt", "new content")}
	writer := &publicationEvents{}
	logs, err := UploadOutputs(context.Background(), outputs, store, events.NewTaskWriter("task", 0, writer), 2)
	if err == nil {
		t.Fatal("partial publication failure returned no error")
	}
	if len(logs) != 1 || logs[0].Url != outputs[0].Url || logs[0].Path != outputs[0].Path || logs[0].SizeBytes != "9" {
		t.Fatalf("successful output logs: %+v", logs)
	}
	if writer.count("upload finished") != 1 || writer.count("upload failed") != 1 {
		t.Fatalf("publication events: %v", writer.snapshot())
	}
	if reads.Load() != 0 {
		t.Fatal("publication inspected stale destination metadata")
	}
}

func TestUploadOutputsRetriesProduceOneSuccessLog(t *testing.T) {
	var attempts atomic.Int32
	store := workerPublicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("unexpected request %s", r.Method)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>temporary denial</Message></Error>`)
			return
		}
		w.Header().Set("ETag", `"retry-receipt"`)
		w.WriteHeader(http.StatusOK)
	})
	retry := util.NewRetrier()
	retry.InitialInterval, retry.MaxInterval, retry.RandomizationFactor, retry.MaxTries = time.Millisecond, time.Millisecond, 0, 2
	writer := &publicationEvents{}
	output := workerOutputFile(t, "retry.txt", "retried")
	logs, err := UploadOutputs(context.Background(), []*tes.Output{output}, &storage.Retrier{Retrier: retry, Backend: store}, events.NewTaskWriter("task", 0, writer), 1)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || len(logs) != 1 || logs[0].SizeBytes != "7" || logs[0].Url != output.Url {
		t.Fatalf("attempts=%d logs=%+v", attempts.Load(), logs)
	}
	if writer.count("upload started") != 1 || writer.count("upload finished") != 1 || writer.count("upload failed") != 0 {
		t.Fatalf("retry produced duplicate transfer events: %v", writer.snapshot())
	}
}

func TestUploadOutputsCancellationDoesNotReportSuccess(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	store := workerPublicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &publicationEvents{}
	output := workerOutputFile(t, "cancelled.txt", "unacknowledged")
	type result struct {
		logs []*tes.OutputFileLog
		err  error
	}
	done := make(chan result, 1)
	go func() {
		logs, err := UploadOutputs(ctx, []*tes.Output{output}, store, events.NewTaskWriter("task", 0, writer), 1)
		done <- result{logs, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	cancel()
	select {
	case got := <-done:
		if len(got.logs) != 0 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancelled publication returned logs=%+v error=%v", got.logs, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled output upload did not return")
	}
	if writer.count("upload finished") != 0 || writer.count("upload failed") != 1 {
		t.Fatalf("cancelled upload events: %v", writer.snapshot())
	}
}

func TestUploadOutputsAlreadyCancelledMakesNoWrite(t *testing.T) {
	var requests atomic.Int32
	store := workerPublicationS3(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusOK) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := &publicationEvents{}
	logs, err := UploadOutputs(ctx, []*tes.Output{workerOutputFile(t, "cancelled.txt", "output")}, store, events.NewTaskWriter("task", 0, writer), 1)
	if len(logs) != 0 || !errors.Is(err, context.Canceled) || requests.Load() != 0 || len(writer.snapshot()) != 0 {
		t.Fatalf("cancelled submission logs=%+v error=%v requests=%d events=%v", logs, err, requests.Load(), writer.snapshot())
	}
}

type publicationStore struct{ storage.Storage }

func (publicationStore) UnsupportedOperations(string) storage.UnsupportedOperations {
	return storage.AllSupported()
}

func TestDefaultWorkerWaitsForOutputAcknowledgementBeforeCompletion(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "rejected"}[rejected], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			store := workerPublicationS3(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if rejected {
					w.WriteHeader(http.StatusForbidden)
					io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
					return
				}
				w.Header().Set("ETag", `"confirmed-write"`)
				w.WriteHeader(http.StatusOK)
			})
			base := t.TempDir()
			workdir := filepath.Join(base, "task")
			if err := os.MkdirAll(workdir, 0700); err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(workdir, "outputs", "result.txt")
			if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(local, []byte("worker output"), 0600); err != nil {
				t.Fatal(err)
			}
			task := &tes.Task{Id: "task", State: tes.Queued, Outputs: []*tes.Output{{Url: "s3://outputs/result.txt", Path: "/outputs/result.txt", Type: tes.File.Enum()}}}
			writer := &publicationEvents{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker := &DefaultWorker{Conf: &config.Worker{WorkDir: base, PollingRate: durationpb.New(time.Hour), MaxParallelTransfers: 1}, Store: publicationStore{store}, TaskReader: &Base64TaskReader{task: task}, EventWriter: writer}
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start output upload")
			}
			for _, ev := range writer.snapshot() {
				if state, ok := ev.Data.(*events.Event_State); ok && tes.TerminalState(state.State) {
					t.Fatalf("terminal state before upload response: %v", state.State)
				}
			}
			if _, err := os.Stat(local); err != nil {
				t.Fatalf("worker cleaned workspace before upload response: %v", err)
			}
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				if (err != nil) != rejected {
					t.Fatalf("worker error=%v rejected=%v", err, rejected)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not finish after upload response")
			}
			outputIndex, terminalIndex := -1, -1
			var terminal tes.State
			for index, ev := range writer.snapshot() {
				if ev.GetOutputs() != nil {
					outputIndex = index
					values := ev.GetOutputs().Value
					if len(values) != 1 || values[0].Path != "/outputs/result.txt" || values[0].SizeBytes != "13" {
						t.Fatalf("worker output log: %+v", values)
					}
				}
				if state, ok := ev.Data.(*events.Event_State); ok && tes.TerminalState(state.State) {
					terminalIndex, terminal = index, state.State
				}
			}
			if rejected {
				if terminal != tes.SystemError || outputIndex != -1 {
					t.Fatalf("rejected output reported state=%v outputIndex=%d", terminal, outputIndex)
				}
			} else if terminal != tes.Complete || outputIndex < 0 || outputIndex >= terminalIndex {
				t.Fatalf("publication ordering output=%d terminal=%d state=%v", outputIndex, terminalIndex, terminal)
			}
			if _, err := os.Stat(workdir); !os.IsNotExist(err) {
				t.Fatalf("worker workspace was not cleaned after finalization: %v", err)
			}
		})
	}
}

func TestWorkerOutputFailureIncludesTaskPathAndOriginalError(t *testing.T) {
	root := t.TempDir()
	mapper := NewFileMapper(filepath.Join(root, "work"))
	output := &tes.Output{Url: "file://" + filepath.Join(root, "destination", "result.txt"), Path: "/outputs/result.txt", Type: tes.File.Enum()}
	if err := mapper.MapTask(&tes.Task{Outputs: []*tes.Output{output}}, "docker"); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(&config.LocalStorage{AllowedDirs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	writer := &publicationEvents{}
	run := &helper{}
	w := &DefaultWorker{Conf: &config.Worker{WorkDir: root, MaxParallelTransfers: 1}, Store: store}
	w.processOutputs(context.Background(), events.NewTaskWriter("task", 0, writer), mapper, run)
	if !errors.Is(run.syserr, os.ErrNotExist) {
		t.Fatalf("expected original missing-file error, got %v", run.syserr)
	}
	for _, event := range writer.snapshot() {
		log := event.GetSystemLog()
		if log.GetMsg() != "upload failed" {
			continue
		}
		fields := log.GetFields()
		if fields["path"] != output.Path {
			t.Errorf("output path = %q, want %q", fields["path"], output.Path)
		}
		if !strings.Contains(fields["error"], mapper.Outputs[0].Path) {
			t.Errorf("original error missing host path: %q", fields["error"])
		}
		return
	}
	t.Fatal("missing upload failure event")
}

type failingOutputStore struct {
	storage.Fake
	err error
}

func (s failingOutputStore) Put(context.Context, string, string) (*storage.Object, error) {
	return nil, s.err
}

func TestWorkerOutputFailurePreservesStorageError(t *testing.T) {
	root := t.TempDir()
	mapper := NewFileMapper(filepath.Join(root, "work"))
	output := &tes.Output{Url: "s3://bucket/result.txt", Path: "/outputs/result.txt", Type: tes.File.Enum()}
	if err := mapper.MapTask(&tes.Task{Outputs: []*tes.Output{output}}, "docker"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapper.Outputs[0].Path, []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("storage access denied")
	writer := &publicationEvents{}
	run := &helper{}
	w := &DefaultWorker{Conf: &config.Worker{WorkDir: root, MaxParallelTransfers: 1}, Store: failingOutputStore{err: denied}}
	w.processOutputs(context.Background(), events.NewTaskWriter("task", 0, writer), mapper, run)
	if !errors.Is(run.syserr, denied) {
		t.Fatalf("expected original storage error, got %v", run.syserr)
	}
	for _, event := range writer.snapshot() {
		log := event.GetSystemLog()
		if log.GetMsg() == "upload failed" {
			if fields := log.GetFields(); fields["path"] != output.Path || fields["error"] != denied.Error() {
				t.Errorf("unexpected failure details: %v", fields)
			}
			return
		}
	}
	t.Fatal("missing upload failure event")
}

func TestProcessOutputsWarnsAndPublishesOtherOutputsForUnmatchedGlob(t *testing.T) {
	executorErr := errors.New("executor failed")
	for _, tc := range []struct {
		name    string
		execErr error
	}{
		{name: "executor succeeds"},
		{name: "executor fails", execErr: executorErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "task")
			globDir := filepath.Join(workDir, "outputs", "glob")
			if err := os.MkdirAll(globDir, 0700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				filepath.Join(globDir, "matched.txt"):          "matched output",
				filepath.Join(workDir, "outputs", "exact.txt"): "exact output",
			}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}

			pattern := filepath.Join(workDir, "outputs", "missing-*.txt")
			mapper := NewFileMapper(workDir)
			mapper.Outputs = []*tes.Output{
				{Url: "s3://outputs/missing", Path: pattern, PathPrefix: "/outputs", Type: tes.File.Enum()},
				{Url: "s3://outputs", Path: filepath.Join(globDir, "*.txt"), PathPrefix: "/outputs", Type: tes.File.Enum()},
				{Url: "s3://outputs/exact.txt", Path: filepath.Join(workDir, "outputs", "exact.txt"), Type: tes.File.Enum()},
			}
			store := &capturingUploadStorage{uploaded: make(map[string]string)}
			writer := &publicationEvents{}
			run := &helper{execerr: tc.execErr}
			worker := &DefaultWorker{Conf: &config.Worker{MaxParallelTransfers: 1}, Store: store}
			worker.processOutputs(context.Background(), events.NewTaskWriter("task", 0, writer), mapper, run)

			if run.syserr != nil {
				t.Fatalf("unmatched glob set system error: %v", run.syserr)
			}
			if run.execerr != tc.execErr {
				t.Fatalf("executor error = %v, want %v", run.execerr, tc.execErr)
			}
			if len(store.uploaded) != 2 || store.uploaded["s3://outputs/glob/matched.txt"] != "matched output" || store.uploaded["s3://outputs/exact.txt"] != "exact output" {
				t.Fatalf("uploaded outputs = %v", store.uploaded)
			}
			if len(store.attempted) != 2 {
				t.Fatalf("upload attempts = %v, want only the matched glob and exact output", store.attempted)
			}

			warningCount := 0
			for _, ev := range writer.snapshot() {
				log := ev.GetSystemLog()
				if log.GetMsg() != "output pattern matched no files" {
					continue
				}
				warningCount++
				if log.GetLevel() != "warning" {
					t.Errorf("unmatched pattern log level = %q, want warning", log.GetLevel())
				}
				if got, want := log.GetFields()["path"], mapper.ContainerPath(pattern); got != want {
					t.Errorf("warning path = %q, want container path %q", got, want)
				}
			}
			if warningCount != 1 {
				t.Fatalf("unmatched output warnings = %d, want 1; events = %v", warningCount, writer.snapshot())
			}
		})
	}
}

func TestProcessOutputsDoesNotWarnBeforeHardResolutionError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		badPattern string
		store      func() storage.Storage
	}{
		{
			name:       "malformed glob",
			badPattern: "bad-[*.txt",
			store:      func() storage.Storage { return &capturingUploadStorage{uploaded: make(map[string]string)} },
		},
		{
			name:       "storage join error",
			badPattern: "*.txt",
			store:      func() storage.Storage { return &storage.Mux{} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			if tc.name == "storage join error" {
				if err := os.WriteFile(filepath.Join(workDir, "output.txt"), []byte("output"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			mapper := NewFileMapper(workDir)
			mapper.Outputs = []*tes.Output{
				{Url: "s3://outputs/missing", Path: filepath.Join(workDir, "missing-*.txt")},
				{Url: "gs://outputs/error", Path: filepath.Join(workDir, tc.badPattern)},
			}
			originalOutputs := append([]*tes.Output(nil), mapper.Outputs...)
			writer := &publicationEvents{}
			worker := &DefaultWorker{Conf: &config.Worker{MaxParallelTransfers: 1}, Store: tc.store()}
			run := &helper{}
			worker.processOutputs(context.Background(), events.NewTaskWriter("task", 0, writer), mapper, run)

			if run.syserr == nil {
				t.Fatal("hard output resolution error was not reported")
			}
			for i := range originalOutputs {
				if mapper.Outputs[i] != originalOutputs[i] {
					t.Fatalf("output %d changed after hard resolution error", i)
				}
			}
			for _, ev := range writer.snapshot() {
				if ev.GetSystemLog().GetMsg() == "output pattern matched no files" {
					t.Fatalf("emitted unmatched warning before hard error: %v", ev.GetSystemLog())
				}
			}
		})
	}
}
