package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
)

func TestDownloadMountedInputDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "a b#%.txt"), []byte("input-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a%20b.txt"), []byte("literal percent key"), 0600); err != nil {
		t.Fatal(err)
	}
	conf := config.EmptyConfig()
	conf.GenericS3 = []*config.GenericS3Storage{{Endpoint: "http://s3.invalid", Bucket: "bucket", Key: "key", Secret: "secret"}}
	store, err := storage.NewWorkerMux(conf, []storage.S3ReadMount{{Endpoint: "http://s3.invalid", Bucket: "bucket", Prefix: "ref", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "inputs")
	ev := events.NewTaskWriter("mounted", 0, events.Noop{})
	if err := DownloadInputs(context.Background(), []*tes.Input{{Url: "s3://bucket/ref", Path: dst, Type: tes.Directory}}, store, ev, 2); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "nested", "a b#%.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "input-data" {
		t.Fatalf("wrong copied input: %q", data)
	}
	literal, err := os.ReadFile(filepath.Join(dst, "a%20b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(literal) != "literal percent key" {
		t.Fatalf("literal S3 key changed during transfer: %q", literal)
	}
}

func TestFlattenInputsRejectsOutsideDirectory(t *testing.T) {
	for _, child := range []string{"s3://bucket/elsewhere/file", "s3://other/ref/file", "s3://bucket/ref/../file"} {
		t.Run(child, func(t *testing.T) {
			_, err := FlattenInputs(context.Background(), []*tes.Input{{Url: "s3://bucket/ref", Path: t.TempDir(), Type: tes.Directory}}, fakelist{list: []*storage.Object{{URL: child}}}, events.NewTaskWriter("mounted", 0, events.Noop{}))
			if err == nil {
				t.Fatal("directory listing escaped input destination")
			}
		})
	}
}

func TestFlattenInputsListsS3DirectoryBySlashQualifiedPrefix(t *testing.T) {
	for _, directoryURL := range []string{"s3://bucket/dir", "s3://bucket/dir/"} {
		t.Run(directoryURL, func(t *testing.T) {
			requestedPrefixes := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				prefix := r.URL.Query().Get("prefix")
				requestedPrefixes <- prefix
				contents := `<Contents><Key>dir/nested/a.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"nested"</ETag><Size>4</Size><StorageClass>STANDARD</StorageClass></Contents>` +
					`<Contents><Key>dir/root.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"root"</ETag><Size>4</Size><StorageClass>STANDARD</StorageClass></Contents>`
				if prefix == "dir" {
					contents += `<Contents><Key>directory.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"sibling"</ETag><Size>7</Size><StorageClass>STANDARD</StorageClass></Contents>`
				}
				w.Header().Set("Content-Type", "application/xml")
				if _, err := fmt.Fprintf(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bucket</Name><Prefix>%s</Prefix><IsTruncated>false</IsTruncated>%s</ListBucketResult>`, prefix, contents); err != nil {
					t.Errorf("write S3 list response: %v", err)
				}
			}))
			defer server.Close()

			store, err := storage.NewGenericS3(&config.GenericS3Storage{Endpoint: server.URL, Region: "us-east-1"})
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "inputs")
			flat, err := FlattenInputs(context.Background(), []*tes.Input{{Url: directoryURL, Path: destination, Type: tes.Directory}}, store, events.NewTaskWriter("task", 0, events.Noop{}))

			var requestedPrefix string
			select {
			case requestedPrefix = <-requestedPrefixes:
			default:
				t.Fatal("GenericS3 did not issue a ListObjects request")
			}
			if requestedPrefix != "dir/" {
				t.Fatalf("ListObjects prefix = %q, want %q", requestedPrefix, "dir/")
			}
			if err != nil {
				t.Fatalf("flatten directory input: %v", err)
			}

			want := []*tes.Input{
				{Url: "s3://bucket/dir/nested/a.txt", Path: filepath.Join(destination, "nested", "a.txt")},
				{Url: "s3://bucket/dir/root.txt", Path: filepath.Join(destination, "root.txt")},
			}
			if !reflect.DeepEqual(flat, want) {
				t.Fatalf("flattened inputs = %#v, want %#v", flat, want)
			}
			select {
			case extraPrefix := <-requestedPrefixes:
				t.Fatalf("unexpected extra ListObjects request with prefix %q", extraPrefix)
			default:
			}
		})
	}
}
