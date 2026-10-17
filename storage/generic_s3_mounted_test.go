package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/calypr/funnel/config"
)

type mountedAPI struct {
	mu    sync.Mutex
	calls []string
}

func (a *mountedAPI) count() int   { a.mu.Lock(); defer a.mu.Unlock(); return len(a.calls) }
func (a *mountedAPI) last() string { a.mu.Lock(); defer a.mu.Unlock(); return a.calls[len(a.calls)-1] }

func mountedFixture(t *testing.T) (*GenericS3, *mountedAPI, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "a b.txt"), []byte("mounted"), 0600); err != nil {
		t.Fatal(err)
	}
	api := &mountedAPI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.calls = append(api.calls, r.Method+":"+r.URL.Path)
		api.mu.Unlock()
		if r.Method == http.MethodPut {
			w.Header().Set("ETag", `"api-receipt"`)
			return
		}
		if r.URL.Query().Has("list-type") {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated></ListBucketResult>`)
			return
		}
		w.Header().Set("ETag", `"remote"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Length", "6")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, "remote")
		}
	}))
	t.Cleanup(server.Close)
	conf := config.EmptyConfig()
	conf.AmazonS3.Disabled = true
	conf.GoogleStorage.Disabled = true
	conf.GenericS3 = []*config.GenericS3Storage{{Endpoint: server.URL, Bucket: "bucket", Key: "key", Secret: "secret", Region: "us-east-1"}}
	mux, err := NewWorkerMux(conf, []S3ReadMount{{Endpoint: server.URL, Bucket: "bucket", Prefix: "reference/", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	return mux.Backends[0].(*GenericS3), api, root
}

func TestGenericS3MountedReadsCopyAndList(t *testing.T) {
	s, fallback, root := mountedFixture(t)
	ctx := context.Background()
	objects, err := s.List(ctx, "s3://bucket/reference/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].URL != "s3://bucket/reference/nested/a b.txt" {
		t.Fatalf("incorrect listing: %+v", objects)
	}
	dst := filepath.Join(t.TempDir(), "input")
	obj, err := s.Get(ctx, objects[0].URL, dst)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "mounted" || obj.Size != 7 || fallback.count() != 0 {
		t.Fatalf("read did not use mount: %q %+v %v", data, obj, fallback.count())
	}
	if err := os.WriteFile(dst, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "nested", "a b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "mounted" {
		t.Fatal("copy aliased source instead of making a workspace copy")
	}
	if _, err := s.Get(ctx, "s3://bucket/reference/missing", dst); err == nil {
		t.Fatal("missing mounted input silently fell back")
	}
	if fallback.count() != 0 {
		t.Fatal("matched missing input reached remote backend")
	}
}

func TestGenericS3MountedReadsFallbackAndAPIPublication(t *testing.T) {
	s, api, root := mountedFixture(t)
	store := &Mux{Backends: []Storage{s}}
	ctx := context.Background()
	dst := filepath.Join(t.TempDir(), "input")
	if _, err := store.Get(ctx, "s3://bucket/reference-other/file", dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "remote" {
		t.Fatalf("API fallback bytes %q, error %v", data, err)
	}
	if api.count() == 0 {
		t.Fatal("unmatched input bypassed S3 API")
	}
	obj, err := store.Put(ctx, "s3://bucket/reference/result", filepath.Join(root, "nested", "a b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if obj.ETag != "api-receipt" || api.last() != "PUT:/bucket/reference/result" {
		t.Fatalf("output bypassed API: %+v %s", obj, api.last())
	}
	if _, err := os.Stat(filepath.Join(root, "result")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output wrote into mount: %v", err)
	}
}

func TestGenericS3MountedReadsRejectEscapes(t *testing.T) {
	s, fallback, root := mountedFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"s3://bucket/reference/../secret", "s3://bucket/reference/%2e%2e/secret", "s3://bucket/reference/x%2f..%2fsecret", "s3://bucket/reference/escape/secret", "s3://bucket/reference/%5csecret", "s3://bucket/reference/%00"} {
		t.Run(u, func(t *testing.T) {
			if _, err := s.Get(context.Background(), u, filepath.Join(t.TempDir(), "out")); err == nil {
				t.Fatal("unsafe input accepted")
			}
			if _, err := s.Stat(context.Background(), u); err == nil {
				t.Fatal("unsafe stat accepted")
			}
		})
	}
	if _, err := s.List(context.Background(), "s3://bucket/reference"); err == nil {
		t.Fatal("listing followed escaping symlink")
	}
	if fallback.count() != 0 {
		t.Fatal("unsafe input fell back to API")
	}
}

func TestGenericS3MountedReadReplacesDestinationAliases(t *testing.T) {
	s, _, root := mountedFixture(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	linkDestination := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(secret, linkDestination); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), "s3://bucket/reference/nested/a b.txt", linkDestination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(secret)
	if err != nil || string(got) != "must remain" {
		t.Fatalf("mounted read followed a destination symlink: %q, %v", got, err)
	}

	aliasDestination := filepath.Join(t.TempDir(), "hardlink")
	alias := filepath.Join(outside, "alias")
	if err := os.WriteFile(alias, []byte("preserve alias"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(alias, aliasDestination); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), "s3://bucket/reference/nested/a b.txt", aliasDestination); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(alias)
	if err != nil || string(got) != "preserve alias" {
		t.Fatalf("mounted read changed a destination hardlink: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "a b.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestMountedReadValidationDoesNotRestrictAPIOutputs(t *testing.T) {
	s, fallback, root := mountedFixture(t)
	u := "s3://bucket/reference/%2e%2e/result"
	unsupported := s.UnsupportedOperations(u)
	if unsupported.Get == nil || unsupported.Stat == nil || unsupported.List == nil {
		t.Fatal("unsafe mounted reads were permitted")
	}
	if unsupported.Put != nil || unsupported.Join != nil {
		t.Fatalf("mounted read policy blocked API outputs: %+v", unsupported)
	}
	obj, err := s.Put(context.Background(), u, filepath.Join(root, "nested", "a b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if obj.ETag != "api-receipt" || fallback.count() != 2 || fallback.last() != "PUT:/bucket/reference/%2e%2e/result" {
		t.Fatalf("output did not use the API backend: %+v %v", obj, fallback.count())
	}
}

func TestGenericS3MountedReadsCancellationAndValidation(t *testing.T) {
	s, _, root := mountedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Get(ctx, "s3://bucket/reference/nested/a b.txt", filepath.Join(t.TempDir(), "out")); !errors.Is(err, context.Canceled) {
		t.Fatalf("get: %v", err)
	}
	if _, err := s.List(ctx, "s3://bucket/reference"); !errors.Is(err, context.Canceled) {
		t.Fatalf("list: %v", err)
	}
	for _, mounts := range [][]S3ReadMount{
		{{Endpoint: "http://s3", Bucket: "", Root: root}},
		{{Endpoint: "http://s3", Bucket: "bucket", Root: "relative"}},
		{{Endpoint: "http://s3", Bucket: "bucket", Prefix: "../bad", Root: root}},
		{{Endpoint: "http://s3", Bucket: "bucket?query", Root: root}},
		{{Endpoint: "http://s3", Bucket: "bucket", Prefix: "/ref", Root: root}},
		{{Endpoint: "http://s3", Bucket: "bucket", Root: root}, {Endpoint: "http://s3", Bucket: "bucket", Prefix: "other", Root: root}},
	} {
		if err := ValidateS3ReadMounts(mounts); err == nil {
			t.Fatalf("invalid mounted backend accepted: %+v", mounts)
		}
	}
	if err := ValidateS3ReadMounts([]S3ReadMount{{Endpoint: "http://s3", Bucket: "bucket", Root: root}, {Endpoint: "http://s3", Bucket: "other", Root: root}}); err != nil {
		t.Fatal(err)
	}
}
