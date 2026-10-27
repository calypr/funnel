package storage

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
	"time"

	"github.com/calypr/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestHTTP(t *testing.T) {
	store, err := NewHTTP(&config.HTTPStorage{
		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{
				Duration: durationpb.New(time.Second * 2),
			},
		},
	})
	if err != nil {
		t.Fatal("Error creating HTTP backend:", err)
	}

	// test Get is supported
	err = store.UnsupportedOperations("https://google.com").Get
	if err != nil {
		t.Error("Unexpected error for unsupported.Get call:", err)
	}

	// test Stat is supported
	err = store.UnsupportedOperations("https://google.com").Stat
	if err != nil {
		t.Error("Unexpected error for unsupported.Stat call:", err)
	}

	// test Get succeeds
	_, err = store.Get(context.Background(), "https://google.com", "_test_download/test_https_download.html")
	if err != nil {
		t.Error("Unexpected error for Get call:", err)
	}

	// test Stat succeeds
	_, err = store.Stat(context.Background(), "https://google.com")
	if err != nil {
		t.Error("Unexpected error for Stat call:", err)
	}

	// test Get on file that requires auth; expect error
	_, err = store.Get(context.Background(), "https://s3.amazonaws.com/private", "_test_download/test_https_download_private.html")
	if err == nil {
		t.Error("Expected error for Get call")
	}

	// test client timeout
	_, err = store.Stat(context.Background(), "https://fakeurl1234.com")
	if err == nil {
		t.Error("Expected timeout error")
	}

	// test List not supported
	_, err = store.List(context.Background(), "https://google.com")
	if err == nil {
		t.Error("Expected error for List call")
	}

	// test Put is supported
	err = store.UnsupportedOperations("https://google.com").Put
	if err != nil {
		t.Error("Unexpected error for unsupported.Put call:", err)
	}

	// test Put to an unreachable host fails
	_, err = store.Put(context.Background(), "https://fakeurl1234.com", "_test_download/test_https_download.html")
	if err == nil {
		t.Error("Expected error for Put call")
	}
}

func TestHTTPTransferTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("first"))
			w.(http.Flusher).Flush()
			time.Sleep(250 * time.Millisecond)
			_, _ = w.Write([]byte("second"))
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			time.Sleep(250 * time.Millisecond)
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name      string
		timeout   *config.TimeoutConfig
		wantError bool
	}{
		{"default", config.DefaultConfig().HTTPStorage.Timeout, false},
		{"explicit total deadline", &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(75 * time.Millisecond)}}, true},
	} {
		for _, method := range []string{"GET", "PUT"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				store, err := NewHTTP(&config.HTTPStorage{Timeout: tc.timeout})
				if err != nil {
					t.Fatal(err)
				}
				var transferErr error
				if method == "GET" {
					file := filepath.Join(t.TempDir(), "input")
					_, transferErr = store.Get(context.Background(), server.URL, file)
					if transferErr == nil {
						data, err := os.ReadFile(file)
						if err != nil || string(data) != "firstsecond" {
							t.Fatalf("download = %q, error = %v", data, err)
						}
					}
				} else {
					_, transferErr = store.Put(context.Background(), server.URL, writeTestFile(t, "upload"))
				}
				if (transferErr != nil) != tc.wantError {
					t.Fatalf("transfer error = %v, want error = %v", transferErr, tc.wantError)
				}
			})
		}
	}
}

func TestHTTPUploadCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	store, err := NewHTTP(config.DefaultConfig().HTTPStorage)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = store.Put(ctx, server.URL, writeTestFile(t, "upload"))
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestHTTPDelayedResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("complete"))
	}))
	defer server.Close()
	store, err := NewHTTP(config.DefaultConfig().HTTPStorage)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "input")
	if _, err := store.Get(context.Background(), server.URL, file); err != nil {
		t.Fatalf("delayed headers: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "complete" {
		t.Fatalf("download = %q, error = %v", data, err)
	}
}

func TestHTTPResponseHeaderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	store, err := NewHTTP(config.DefaultConfig().HTTPStorage)
	if err != nil {
		t.Fatal(err)
	}
	transport := store.client.Transport.(*http.Transport)
	if transport.ResponseHeaderTimeout != time.Minute {
		t.Fatalf("header timeout = %s, want 1 minute", transport.ResponseHeaderTimeout)
	}
	transport.ResponseHeaderTimeout = 50 * time.Millisecond
	_, err = store.Get(context.Background(), server.URL, filepath.Join(t.TempDir(), "input"))
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected response header timeout, got %v", err)
	}
}

func newTestHTTP(t *testing.T) *HTTP {
	t.Helper()
	store, err := NewHTTP(&config.HTTPStorage{
		Timeout: &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Duration{
				Duration: durationpb.New(time.Second * 5),
			},
		},
	})
	if err != nil {
		t.Fatal("Error creating HTTP backend:", err)
	}
	return store
}

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal("Error writing test file:", err)
	}
	return path
}

// The query mimics an S3 presigned URL, whose signature covers the exact
// query string, so it must reach the server unchanged.
const presignedQuery = "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIA%2F20261023%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Expires=900&X-Amz-SignedHeaders=host&X-Amz-Signature=abc123"

func TestHTTPPutUploadsWithContentLength(t *testing.T) {
	content := "hello presigned world\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.RawQuery != presignedQuery {
			t.Errorf("query = %q, want it unchanged", r.URL.RawQuery)
		}
		// Object stores such as S3 reject chunked uploads.
		if len(r.TransferEncoding) != 0 {
			t.Errorf("transfer encoding = %v, want none", r.TransferEncoding)
		}
		if r.ContentLength != int64(len(content)) {
			t.Errorf("content length = %d, want %d", r.ContentLength, len(content))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != content {
			t.Errorf("body = %q, want %q", body, content)
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	url := server.URL + "/bucket/run/output.txt?" + presignedQuery
	obj, err := newTestHTTP(t).Put(context.Background(), url, writeTestFile(t, content))
	if err != nil {
		t.Fatal("Unexpected error for Put call:", err)
	}
	if obj.Size != int64(len(content)) {
		t.Errorf("object size = %d, want %d", obj.Size, len(content))
	}
	if obj.ETag != `"etag-1"` {
		t.Errorf("object ETag = %q, want %q", obj.ETag, `"etag-1"`)
	}
	if obj.Name != "/bucket/run/output.txt" {
		t.Errorf("object name = %q, want the path without the signed query", obj.Name)
	}
}

func TestHTTPPutUploadsEmptyFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TransferEncoding) != 0 {
			t.Errorf("transfer encoding = %v, want none", r.TransferEncoding)
		}
		if got := r.Header.Get("Content-Length"); got != "0" {
			t.Errorf("Content-Length header = %q, want \"0\"", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	obj, err := newTestHTTP(t).Put(context.Background(), server.URL+"/empty.txt", writeTestFile(t, ""))
	if err != nil {
		t.Fatal("Unexpected error for Put call:", err)
	}
	if obj.Size != 0 {
		t.Errorf("object size = %d, want 0", obj.Size)
	}
}

func TestHTTPPutReportsRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "<Error><Code>SignatureDoesNotMatch</Code></Error>")
	}))
	defer server.Close()

	_, err := newTestHTTP(t).Put(context.Background(), server.URL+"/output.txt", writeTestFile(t, "data"))
	if err == nil {
		t.Fatal("Expected error for rejected Put call")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("error = %q, want the status code and the server's explanation", err)
	}
}

func TestHTTPPutMissingHostFile(t *testing.T) {
	_, err := newTestHTTP(t).Put(context.Background(), "https://example.com/output.txt", filepath.Join(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("Expected error for Put call with a missing host file")
	}
}

func TestHTTPPutFollowsRedirectWithBody(t *testing.T) {
	content := "redirected upload\n"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.RawQuery != presignedQuery {
			t.Errorf("query = %q, want it unchanged", r.URL.RawQuery)
		}
		if len(r.TransferEncoding) != 0 || r.ContentLength != int64(len(content)) {
			t.Errorf("transfer encoding = %v, content length = %d, want none and %d", r.TransferEncoding, r.ContentLength, len(content))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != content {
			t.Errorf("body = %q, want %q", body, content)
		}
		w.Header().Set("ETag", `"etag-redirected"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// A gateway that creates a presigned URL on demand and redirects to it.
	// It answers without reading the body, which with Expect: 100-continue
	// the client holds back while it waits for that answer.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Expect") != "100-continue" {
			t.Errorf("Expect header = %q, want 100-continue", r.Header.Get("Expect"))
		}
		http.Redirect(w, r, target.URL+"/bucket/output.txt?"+presignedQuery, http.StatusTemporaryRedirect)
	}))
	defer gateway.Close()

	obj, err := newTestHTTP(t).Put(context.Background(), gateway.URL+"/upload/token/output.txt", writeTestFile(t, content))
	if err != nil {
		t.Fatal("Unexpected error for Put call:", err)
	}
	if obj.ETag != `"etag-redirected"` {
		t.Errorf("object ETag = %q, want the redirect target's", obj.ETag)
	}
}

func TestHTTPPutStopsAtRedirectToGet(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// net/http would follow a 302 for a PUT with a GET, and that GET's 200
	// must not pass for a successful upload.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/output.txt", http.StatusFound)
	}))
	defer gateway.Close()

	_, err := newTestHTTP(t).Put(context.Background(), gateway.URL+"/output.txt", writeTestFile(t, "data"))
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("error = %v, want the 302 reported", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target received %d requests, want 0", n)
	}
}

func TestHTTPGetFollowsRedirect(t *testing.T) {
	content := "redirected download\n"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, content)
	}))
	defer target.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/input.txt?"+presignedQuery, http.StatusFound)
	}))
	defer gateway.Close()

	dest := filepath.Join(t.TempDir(), "input.txt")
	if _, err := newTestHTTP(t).Get(context.Background(), gateway.URL+"/download/token/input.txt", dest); err != nil {
		t.Fatal("Unexpected error for Get call:", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != content {
		t.Errorf("downloaded %q, want %q", got, content)
	}
}

func TestHTTPGetStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// The server cancels the download once the request arrives, then waits
	// for the client to give up.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()

	_, err := newTestHTTP(t).Get(ctx, server.URL+"/input.txt", filepath.Join(t.TempDir(), "input.txt"))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want the request canceled with its context", err)
	}
}

func TestHTTPStatStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()

	_, err := newTestHTTP(t).Stat(ctx, server.URL+"/input.txt")
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want the request canceled with its context", err)
	}
}

func TestHTTPJoin(t *testing.T) {
	tests := []struct {
		url, path, want string
	}{
		{"https://example.com/upload/dir", "sub/y.txt", "https://example.com/upload/dir/sub/y.txt"},
		{"https://example.com/upload/dir/", "y.txt", "https://example.com/upload/dir/y.txt"},
		{"https://example.com/upload/dir", "result#1.txt", "https://example.com/upload/dir/result%231.txt"},
		{"https://example.com/upload/dir", "a?b.txt", "https://example.com/upload/dir/a%3Fb.txt"},
		{"https://example.com/upload/dir", "50%25.txt", "https://example.com/upload/dir/50%2525.txt"},
		{"https://example.com/upload/dir", "100%.txt", "https://example.com/upload/dir/100%25.txt"},
		// A server that signs uploads as they arrive may take a token in the
		// query, which must reach it unchanged.
		{"https://example.com/upload/dir?token=a%2Bb", "y.txt", "https://example.com/upload/dir/y.txt?token=a%2Bb"},
	}
	store := newTestHTTP(t)
	for _, tt := range tests {
		got, err := store.Join(tt.url, tt.path)
		if err != nil {
			t.Errorf("Join(%q, %q) returned error: %s", tt.url, tt.path, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tt.url, tt.path, got, tt.want)
		}
	}
}

// Each file of a DIRECTORY output is uploaded to a URL made with Join, and
// must arrive under its own name.
func TestHTTPPutKeepsJoinedFileNames(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.Path
	}))
	defer server.Close()

	store := newTestHTTP(t)
	file := writeTestFile(t, "data")
	for _, name := range []string{"result#1.txt", "a?b.txt", "50%25.txt", "100%.txt", "sub/y.txt"} {
		url, err := store.Join(server.URL+"/upload/dir", name)
		if err != nil {
			t.Fatal("Unexpected error for Join call:", err)
		}
		if _, err := store.Put(context.Background(), url, file); err != nil {
			t.Fatal("Unexpected error for Put call:", err)
		}
		if got := <-received; got != "/upload/dir/"+name {
			t.Errorf("%q was uploaded as %q", name, got)
		}
	}
}
