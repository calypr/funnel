package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
)

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
				fmt.Fprintf(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bucket</Name><Prefix>%s</Prefix><IsTruncated>false</IsTruncated>%s</ListBucketResult>`, prefix, contents)
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
