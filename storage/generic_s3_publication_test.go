package storage

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

	"github.com/ohsu-comp-bio/funnel/config"
)

func publicationS3(t *testing.T, handler http.HandlerFunc) *GenericS3 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := NewGenericS3(&config.GenericS3Storage{Endpoint: server.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func publicationFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGenericS3PutReturnsUploadReceipt(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var uploaded string
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, r.Method)
		if r.Method == http.MethodPut {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			uploaded = string(data)
			w.Header().Set("ETag", `"write-receipt"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("ETag", `"stale-destination"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusOK)
	})
	obj, err := store.Put(context.Background(), "s3://outputs/task/result.txt", publicationFile(t, "new output"))
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "s3://outputs/task/result.txt" || obj.Name != "task/result.txt" || obj.ETag != "write-receipt" || obj.Size != 10 {
		t.Fatalf("unexpected upload receipt: %+v", obj)
	}
	mu.Lock()
	defer mu.Unlock()
	if uploaded != "new output" {
		t.Fatalf("uploaded body %q", uploaded)
	}
	if len(methods) != 1 || methods[0] != http.MethodPut {
		t.Fatalf("receipt required unexpected destination requests: %v", methods)
	}
}

func TestGenericS3PutFailedWriteCannotUseStaleDestination(t *testing.T) {
	var reads atomic.Int32
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>write denied</Message></Error>`)
			return
		}
		reads.Add(1)
		w.Header().Set("ETag", `"old-output"`)
		w.WriteHeader(http.StatusOK)
	})
	obj, err := store.Put(context.Background(), "s3://outputs/task/result.txt", publicationFile(t, "new output"))
	if err == nil || obj != nil {
		t.Fatalf("failed write reported publication: object=%+v error=%v", obj, err)
	}
	if reads.Load() != 0 {
		t.Fatalf("failed write inspected stale destination %d times", reads.Load())
	}
}

func TestGenericS3PutDirectoryAcknowledgesEveryFile(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "all acknowledged", true: "nested write rejected"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range map[string]string{"a.txt": "one", "b.txt": "second"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var uploaded []string
			store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Errorf("unexpected directory publication request: %s", r.Method)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				uploaded = append(uploaded, r.URL.Path)
				if fail && r.URL.Path == "/outputs/task/b.txt" {
					w.WriteHeader(http.StatusForbidden)
					io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
					return
				}
				w.Header().Set("ETag", `"receipt"`)
				w.WriteHeader(http.StatusOK)
			})
			obj, err := store.Put(context.Background(), "s3://outputs/task", dir)
			if fail {
				if err == nil || obj != nil {
					t.Fatalf("directory with failed file succeeded: object=%+v error=%v", obj, err)
				}
			} else if err != nil || obj.Size != 9 || obj.URL != "s3://outputs/task" {
				t.Fatalf("directory receipt: object=%+v error=%v", obj, err)
			}
			if len(uploaded) != 2 || uploaded[0] != "/outputs/task/a.txt" || uploaded[1] != "/outputs/task/b.txt" {
				t.Fatalf("directory uploads: %v", uploaded)
			}
		})
	}
}

func TestGenericS3PutCancellationDuringWrite(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	path := publicationFile(t, "output")
	go func() {
		obj, err := store.Put(ctx, "s3://outputs/result.txt", path)
		if obj != nil {
			result <- errors.New("cancelled upload returned success receipt")
			return
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload ignored cancellation")
	}
}

func TestGenericS3PutLocalReadabilityWaitHonorsCancellation(t *testing.T) {
	var requests atomic.Int32
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusOK) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	obj, err := store.Put(ctx, "s3://outputs/result.txt", filepath.Join(t.TempDir(), "not-yet-readable"))
	if obj != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait cancellation returned object=%+v error=%v", obj, err)
	}
	if requests.Load() != 0 {
		t.Fatal("readability wait made a remote request")
	}
}

func TestGenericS3ListPropagatesObjectError(t *testing.T) {
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>list denied</Message></Error>`)
	})
	objects, err := store.List(context.Background(), "s3://outputs/task")
	if err == nil || len(objects) != 0 {
		t.Fatalf("listing failure returned objects=%+v error=%v", objects, err)
	}
}
