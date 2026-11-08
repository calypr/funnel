package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func mountedWriterFixture(t *testing.T, handler http.HandlerFunc) (*Mux, *GenericS3, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	conf := mountedConfig(server.URL, "bucket")
	conf.GenericS3[0].MountedRead = nil
	root := t.TempDir()
	mux, err := NewWorkerMux(conf, nil, S3WriteMount{Endpoint: server.URL, Bucket: "bucket", Prefix: "results", Root: root, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var backend *GenericS3
	for _, store := range mux.Backends {
		if s3, ok := store.(*GenericS3); ok {
			backend = s3
		}
	}
	if backend == nil {
		t.Fatal("missing S3 backend")
	}
	backend.mountedWrite.pollInterval = time.Millisecond
	return mux, backend, root
}

func remoteSize(w http.ResponseWriter, size int64) {
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"remote-version"`)
	w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
}

func TestMountedWriteWaitsForRemoteExistenceAndSize(t *testing.T) {
	var heads atomic.Int32
	mux, _, root := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("mounted output used %s", r.Method)
			w.WriteHeader(500)
			return
		}
		switch heads.Add(1) {
		case 1:
			w.WriteHeader(http.StatusNotFound)
		case 2:
			remoteSize(w, 2)
		default:
			remoteSize(w, 6)
		}
	})
	obj, err := mux.Put(context.Background(), "s3://bucket/results/a b#%.txt", publicationFile(t, "output"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "a b#%.txt"))
	if err != nil || string(data) != "output" {
		t.Fatalf("mounted bytes %q: %v", data, err)
	}
	if heads.Load() != 3 || obj.Size != 6 || obj.ETag != "remote-version" {
		t.Fatalf("premature publication: %+v, HEAD requests %d", obj, heads.Load())
	}
}

func TestMountedWriteAcceptsSameSizedStaleObjectAndEmptyFile(t *testing.T) {
	for _, content := range []string{"new bytes", ""} {
		t.Run(fmt.Sprintf("size-%d", len(content)), func(t *testing.T) {
			var heads atomic.Int32
			mux, _, _ := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) {
				heads.Add(1)
				remoteSize(w, int64(len(content)))
				w.Header().Set("ETag", `"stale-object"`)
			})
			obj, err := mux.Put(context.Background(), "s3://bucket/results/file", publicationFile(t, content))
			if err != nil {
				t.Fatal(err)
			}
			if heads.Load() != 1 || obj.ETag != "stale-object" || obj.Size != int64(len(content)) {
				t.Fatalf("existence-size contract changed: %+v", obj)
			}
		})
	}
}

func TestMountedWriteTimeoutAndPermissionFailure(t *testing.T) {
	for _, status := range []int{404, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			mux, s3, _ := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			s3.mountedWrite.timeout = 20 * time.Millisecond
			if status == http.StatusForbidden {
				s3.mountedWrite.timeout = 5 * time.Second
			}
			obj, err := mux.Put(context.Background(), "s3://bucket/results/file", publicationFile(t, "data"))
			if err == nil || obj != nil {
				t.Fatalf("failed export reported success: %+v %v", obj, err)
			}
			if status == 404 && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing timeout: %v", err)
			}
			if status == 403 && errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("permission failure retried until timeout: %v", err)
			}
		})
	}
}

func TestMountedWriteCancellation(t *testing.T) {
	reached := make(chan struct{})
	mux, _, _ := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := publicationFile(t, "data")
	result := make(chan error, 1)
	go func() { _, err := mux.Put(ctx, "s3://bucket/results/file", source); result <- err }()
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatal("HEAD never started")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop publication")
	}
}

func TestMountedWriteRejectsEscapesAndPreservesSelfCopy(t *testing.T) {
	var heads atomic.Int32
	mux, _, root := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) { heads.Add(1); remoteSize(w, 4) })
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../outside", "%2e%2e/outside", "escape/outside"} {
		if _, err := mux.Put(context.Background(), "s3://bucket/results/"+key, publicationFile(t, "data")); err == nil {
			t.Fatalf("unsafe destination accepted: %s", key)
		}
	}
	if heads.Load() != 0 {
		t.Fatal("unsafe destination reached publication verification")
	}
	if _, err := os.Stat(filepath.Join(outside, "outside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped mount: %v", err)
	}
	source := filepath.Join(root, "same")
	if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Put(context.Background(), "s3://bucket/results/same", source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "data" {
		t.Fatalf("self-copy changed source %q: %v", data, err)
	}
}

func TestMountedWriteUnmatchedOutputUsesAPI(t *testing.T) {
	var puts atomic.Int32
	mux, _, root := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.Header().Set("ETag", `"api-write"`)
		}
	})
	obj, err := mux.Put(context.Background(), "s3://bucket/elsewhere/file", publicationFile(t, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 1 || obj.ETag != "api-write" {
		t.Fatalf("unmatched output did not use API: %+v", obj)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatalf("unmatched output changed mount: %v %v", files, err)
	}
}

func TestValidateS3WriteMountsRejectsOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	for _, mounts := range [][]S3WriteMount{
		{
			{Endpoint: "http://s3-one", Bucket: "one", Prefix: "out/", Root: root, Timeout: time.Second},
			{Endpoint: "http://s3-two", Bucket: "two", Prefix: "out/", Root: root, Timeout: time.Second},
		},
		{
			{Endpoint: "http://s3-one", Bucket: "one", Prefix: "out/", Root: root, Timeout: time.Second},
			{Endpoint: "http://s3-two", Bucket: "two", Prefix: "out/", Root: filepath.Join(root, "nested"), Timeout: time.Second},
		},
	} {
		if err := ValidateS3WriteMounts(mounts); err == nil {
			t.Fatalf("overlapping write roots accepted: %+v", mounts)
		}
	}

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	aliased := []S3WriteMount{
		{Endpoint: "http://s3-one", Bucket: "one", Prefix: "out/", Root: root, Timeout: time.Second},
		{Endpoint: "http://s3-two", Bucket: "two", Prefix: "out/", Root: alias, Timeout: time.Second},
	}
	if err := ValidateS3WriteMounts(aliased); err != nil {
		t.Fatalf("server-side route validation inspected aliased worker paths: %v", err)
	}
	if err := validateS3WriteMountRootAliases(aliased); err == nil {
		t.Fatal("worker startup accepted symlink aliases for write roots")
	}
}
