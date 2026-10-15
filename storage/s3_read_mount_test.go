package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
)

func mountedConfig(endpoint, bucket string) *config.Config {
	c := config.EmptyConfig()
	c.AmazonS3.Disabled = true
	c.GoogleStorage.Disabled = true
	c.GenericS3 = []*config.GenericS3Storage{{Endpoint: endpoint, Bucket: bucket, Key: "key", Secret: "secret", Region: "us-east-1", MountedRead: &config.S3MountedRead{Volume: "reference", Prefix: "ref"}}}
	return c
}

func TestS3MountedMuxOwnsReadsWithoutAPIDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("local bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	c := mountedConfig("http://s3.invalid", "bucket")
	// An unrelated backend would fail network discovery if the mux probed it.
	c.GenericS3 = append(c.GenericS3, &config.GenericS3Storage{Endpoint: "http://unreachable.invalid", Bucket: "other", Key: "key", Secret: "secret"})
	mux, err := NewWorkerMux(c, []S3ReadMount{{Endpoint: "http://s3.invalid", Bucket: "bucket", Prefix: "ref", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"s3://bucket/ref", "s3://s3.invalid/bucket/ref"} {
		t.Run(prefix, func(t *testing.T) {
			objects, err := mux.List(context.Background(), prefix)
			if err != nil {
				t.Fatal(err)
			}
			if len(objects) != 1 || objects[0].URL != prefix+"/input" {
				t.Fatalf("listing changed URL authority: %+v", objects)
			}
			destination := filepath.Join(t.TempDir(), "input")
			if _, err := mux.Get(context.Background(), objects[0].URL, destination); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != "local bytes" {
				t.Fatalf("input bytes %q, error %v", data, err)
			}
		})
	}
}

func TestS3MountedWorkerIdentityAndServerIsolation(t *testing.T) {
	root := t.TempDir()
	mount := S3ReadMount{Endpoint: "http://s3.invalid", Bucket: "bucket", Prefix: "ref", Root: root}
	for _, test := range []struct {
		name   string
		change func(*config.Config)
	}{
		{"endpoint changed", func(c *config.Config) { c.GenericS3[0].Endpoint = "https://s3.invalid" }},
		{"bucket changed", func(c *config.Config) { c.GenericS3[0].Bucket = "other" }},
		{"disabled", func(c *config.Config) { c.GenericS3[0].Disabled = true }},
		{"credentials missing", func(c *config.Config) { c.GenericS3[0].Key = "" }},
		{"ambiguous", func(c *config.Config) { c.GenericS3 = append(c.GenericS3, c.GenericS3[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := mountedConfig("http://s3.invalid", "bucket")
			test.change(c)
			if _, err := NewWorkerMux(c, []S3ReadMount{mount}); err == nil {
				t.Fatal("unavailable or ambiguous snapshot backend accepted")
			}
		})
	}
	c := mountedConfig("http://s3.invalid", "bucket")
	server, err := NewMux(c)
	if err != nil {
		t.Fatal(err)
	}
	emptySnapshot, err := NewWorkerMux(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mux := range []*Mux{server, emptySnapshot} {
		for _, backend := range mux.Backends {
			if s3, ok := backend.(*GenericS3); ok && s3.mountedRead != nil {
				t.Fatal("static config activated local filesystem reads")
			}
		}
	}
}

func TestS3MountedAmbiguousBucketURLRequiresEndpoint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	c := mountedConfig("http://one.invalid", "bucket")
	c.GenericS3 = append(c.GenericS3, &config.GenericS3Storage{Endpoint: "http://two.invalid", Bucket: "bucket", Key: "key", Secret: "secret"})
	mux, err := NewWorkerMux(c, []S3ReadMount{{Endpoint: "http://one.invalid", Bucket: "bucket", Root: root}, {Endpoint: "http://two.invalid", Bucket: "bucket", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Stat(context.Background(), "s3://bucket/input"); err == nil || !strings.Contains(err.Error(), "multiple mounted S3") {
		t.Fatalf("ambiguous bucket-only URL: %v", err)
	}
	if _, err := mux.Stat(context.Background(), "s3://one.invalid/bucket/input"); err != nil {
		t.Fatal(err)
	}
}

func TestMountedReadDoesNotClaimAnotherBackendPublication(t *testing.T) {
	var uploaded atomic.Int32
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer denied.Close()
	available := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			uploaded.Add(1)
			w.Header().Set("ETag", `"available-receipt"`)
		}
	}))
	defer available.Close()
	c := mountedConfig(denied.URL, "bucket")
	c.GenericS3 = append(c.GenericS3, &config.GenericS3Storage{Endpoint: available.URL, Bucket: "bucket", Key: "key", Secret: "secret", Region: "us-east-1"})
	root := t.TempDir()
	source := filepath.Join(root, "result")
	if err := os.WriteFile(source, []byte("output bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	mux, err := NewWorkerMux(c, []S3ReadMount{{Endpoint: denied.URL, Bucket: "bucket", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := mux.Put(context.Background(), "s3://bucket/result", source)
	if err != nil {
		t.Fatal(err)
	}
	if obj.ETag != "available-receipt" || uploaded.Load() != 1 {
		t.Fatalf("wrong API publication: %+v, uploads %d", obj, uploaded.Load())
	}
}
