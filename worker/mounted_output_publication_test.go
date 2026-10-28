package worker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func workerMountedOutputMux(t *testing.T, root string, timeout time.Duration, handler http.Handler) (*storage.Mux, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	conf := config.EmptyConfig()
	conf.AmazonS3.Disabled = true
	conf.GoogleStorage.Disabled = true
	conf.GenericS3 = []*config.GenericS3Storage{{
		Endpoint: server.URL,
		Bucket:   "bucket",
		Key:      "key",
		Secret:   "secret",
		Region:   "us-east-1",
	}}
	mux, err := storage.NewWorkerMux(conf, nil, storage.S3WriteMount{
		Endpoint: server.URL,
		Bucket:   "bucket",
		Prefix:   "outputs",
		Root:     root,
		Timeout:  timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mux, server
}

func TestDefaultWorkerWaitsForMountedOutputHeadBeforeCompletionAndCleanup(t *testing.T) {
	t.Run("matching size completes", func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		var bucketHeads, objectHeads, puts atomic.Int32
		mountRoot := t.TempDir()
		mux, _ := workerMountedOutputMux(t, mountRoot, 10*time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead && r.URL.Path == "/bucket" {
				bucketHeads.Add(1)
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method == http.MethodPut {
				puts.Add(1)
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method != http.MethodHead || r.URL.Path != "/bucket/outputs/result.txt" {
				t.Errorf("unexpected S3 request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			objectHeads.Add(1)
			close(started)
			<-release
			w.Header().Set("Content-Length", "13")
			w.Header().Set("ETag", `"mounted-receipt"`)
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			w.WriteHeader(http.StatusOK)
		}))

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
		task := &tes.Task{Id: "task", State: tes.Queued, Outputs: []*tes.Output{{Url: "s3://bucket/outputs/result.txt", Path: "/outputs/result.txt", Type: tes.File}}}
		writer := &publicationEvents{}
		conf := config.EmptyConfig().Worker
		conf.WorkDir = base
		conf.PollingRate = durationpb.New(time.Hour)
		conf.MaxParallelTransfers = 1
		worker := &DefaultWorker{Conf: conf, Store: mux, TaskReader: &Base64TaskReader{task: task}, EventWriter: writer}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start mounted-output HEAD")
		}
		mounted, err := os.ReadFile(filepath.Join(mountRoot, "result.txt"))
		if err != nil || string(mounted) != "worker output" {
			t.Fatalf("output was not copied to mount before HEAD: data=%q error=%v", mounted, err)
		}
		for _, ev := range writer.snapshot() {
			if state, ok := ev.Data.(*events.Event_State); ok && tes.TerminalState(state.State) {
				t.Fatalf("worker emitted terminal state %v before HEAD response", state.State)
			}
		}
		if _, err := os.Stat(local); err != nil {
			t.Fatalf("worker cleaned workspace before HEAD response: %v", err)
		}
		select {
		case err := <-done:
			t.Fatalf("worker finished before HEAD response: %v", err)
		default:
		}

		releaseOnce.Do(func() { close(release) })
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not finish after matching HEAD response")
		}
		if bucketHeads.Load() != 0 || objectHeads.Load() != 1 || puts.Load() != 0 {
			t.Fatalf("S3 requests: bucket HEADs=%d object HEADs=%d PUTs=%d", bucketHeads.Load(), objectHeads.Load(), puts.Load())
		}
		outputIndex, terminalIndex := -1, -1
		for index, ev := range writer.snapshot() {
			if ev.GetOutputs() != nil {
				outputIndex = index
				values := ev.GetOutputs().Value
				if len(values) != 1 || values[0].Url != "s3://bucket/outputs/result.txt" || values[0].Path != "/outputs/result.txt" || values[0].SizeBytes != "13" {
					t.Fatalf("worker output log: %+v", values)
				}
			}
			if state, ok := ev.Data.(*events.Event_State); ok && tes.TerminalState(state.State) {
				terminalIndex = index
				if state.State != tes.Complete {
					t.Fatalf("terminal state: %v", state.State)
				}
			}
		}
		if outputIndex < 0 || outputIndex >= terminalIndex {
			t.Fatalf("completion preceded output log: output=%d terminal=%d", outputIndex, terminalIndex)
		}
		if _, err := os.Stat(workdir); !os.IsNotExist(err) {
			t.Fatalf("worker workspace was not cleaned after acknowledgement: %v", err)
		}
	})

	t.Run("cancellation interrupts blocked HEAD", func(t *testing.T) {
		started := make(chan struct{})
		var bucketHeads, objectHeads, puts atomic.Int32
		mountRoot := t.TempDir()
		mux, _ := workerMountedOutputMux(t, mountRoot, 10*time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead && r.URL.Path == "/bucket" {
				bucketHeads.Add(1)
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method == http.MethodPut {
				puts.Add(1)
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method != http.MethodHead || r.URL.Path != "/bucket/outputs/result.txt" {
				t.Errorf("unexpected S3 request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			objectHeads.Add(1)
			close(started)
			<-r.Context().Done()
		}))

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
		task := &tes.Task{Id: "task", State: tes.Queued, Outputs: []*tes.Output{{Url: "s3://bucket/outputs/result.txt", Path: "/outputs/result.txt", Type: tes.File}}}
		writer := &publicationEvents{}
		conf := config.EmptyConfig().Worker
		conf.WorkDir = base
		conf.PollingRate = durationpb.New(time.Hour)
		conf.MaxParallelTransfers = 1
		worker := &DefaultWorker{Conf: conf, Store: mux, TaskReader: &Base64TaskReader{task: task}, EventWriter: writer}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start mounted-output HEAD")
		}
		mounted, err := os.ReadFile(filepath.Join(mountRoot, "result.txt"))
		if err != nil || string(mounted) != "worker output" {
			t.Fatalf("output was not copied to mount before HEAD: data=%q error=%v", mounted, err)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled worker returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not stop after cancellation")
		}
		if bucketHeads.Load() != 0 || objectHeads.Load() != 1 || puts.Load() != 0 {
			t.Fatalf("S3 requests: bucket HEADs=%d object HEADs=%d PUTs=%d", bucketHeads.Load(), objectHeads.Load(), puts.Load())
		}
		if writer.count("upload finished") != 0 || writer.count("upload failed") != 1 {
			t.Fatalf("cancelled output events: %v", writer.snapshot())
		}
		for _, ev := range writer.snapshot() {
			if ev.GetOutputs() != nil {
				t.Fatalf("cancelled output emitted logs: %+v", ev.GetOutputs().Value)
			}
		}
		if _, err := os.Stat(workdir); !os.IsNotExist(err) {
			t.Fatalf("worker workspace was not cleaned after cancellation: %v", err)
		}
	})
}

func TestUploadOutputsMountedDirectoryKeepsAcknowledgedFilesOnPartialFailure(t *testing.T) {
	var bucketHeads, objectHeads, puts atomic.Int32
	mountRoot := t.TempDir()
	mux, _ := workerMountedOutputMux(t, mountRoot, 10*time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/bucket" {
			bucketHeads.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodHead {
			t.Errorf("unexpected S3 request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		objectHeads.Add(1)
		switch r.URL.Path {
		case "/bucket/outputs/bundle/a-good.txt":
			w.Header().Set("Content-Length", "9")
			w.Header().Set("ETag", `"mounted-receipt"`)
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			w.WriteHeader(http.StatusOK)
		case "/bucket/outputs/bundle/z-failed.txt":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
		default:
			t.Errorf("unexpected object HEAD path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	source := t.TempDir()
	directory := filepath.Join(source, "bundle")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(directory, "a-good.txt")
	failed := filepath.Join(directory, "z-failed.txt")
	if err := os.WriteFile(good, []byte("good-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failed, []byte("failed-data"), 0600); err != nil {
		t.Fatal(err)
	}
	output := &tes.Output{Url: "s3://bucket/outputs/bundle", Path: directory, Type: tes.Directory}
	writer := &publicationEvents{}
	logs, err := UploadOutputs(context.Background(), []*tes.Output{output}, mux, events.NewTaskWriter("task", 0, writer), 1)
	if err == nil {
		t.Fatal("directory publication failure returned no error")
	}
	if len(logs) != 1 || logs[0].Url != "s3://bucket/outputs/bundle/a-good.txt" || logs[0].Path != good || logs[0].SizeBytes != "9" {
		t.Fatalf("successful output logs: %+v", logs)
	}
	if writer.count("upload finished") != 1 || writer.count("upload failed") != 1 {
		t.Fatalf("directory publication events: %v", writer.snapshot())
	}
	for relative, want := range map[string]string{"bundle/a-good.txt": "good-data", "bundle/z-failed.txt": "failed-data"} {
		data, err := os.ReadFile(filepath.Join(mountRoot, relative))
		if err != nil || string(data) != want {
			t.Errorf("mounted output %s: data=%q error=%v", relative, data, err)
		}
	}
	if bucketHeads.Load() != 0 || objectHeads.Load() != 2 || puts.Load() != 0 {
		t.Fatalf("S3 requests: bucket HEADs=%d object HEADs=%d PUTs=%d", bucketHeads.Load(), objectHeads.Load(), puts.Load())
	}
}

func TestMountedOutputRetryRecordsOneSuccessfulFile(t *testing.T) {
	var attempts atomic.Int32
	root := t.TempDir()
	mux, _ := workerMountedOutputMux(t, root, time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected request %s", r.Method)
			w.WriteHeader(403)
			return
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	}))
	retry := util.NewRetrier()
	retry.InitialInterval, retry.MaxInterval, retry.RandomizationFactor, retry.MaxTries = time.Millisecond, time.Millisecond, 0, 2
	writer := &publicationEvents{}
	output := workerOutputFile(t, "retry.txt", "retried")
	output.Url = "s3://bucket/outputs/retry.txt"
	logs, err := UploadOutputs(context.Background(), []*tes.Output{output}, &storage.Retrier{Retrier: retry, Backend: mux}, events.NewTaskWriter("task", 0, writer), 1)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || len(logs) != 1 || logs[0].Url != output.Url || logs[0].SizeBytes != "7" {
		t.Fatalf("attempts %d, logs %+v", attempts.Load(), logs)
	}
	if writer.count("upload finished") != 1 || writer.count("upload failed") != 0 {
		t.Fatal("retry duplicated publication events")
	}
}
