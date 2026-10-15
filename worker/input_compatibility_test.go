package worker

import (
	"context"
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

func TestFlattenInputsPreservesEndpointQualifiedS3Directory(t *testing.T) {
	requestedPrefixes := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := r.URL.Query().Get("prefix")
		requestedPrefixes <- prefix
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Name>bucket</Name><Prefix>` + prefix + `</Prefix><IsTruncated>false</IsTruncated>` +
			`<Contents><Key>dir/nested/a b#%.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"nested"</ETag><Size>4</Size><StorageClass>STANDARD</StorageClass></Contents>` +
			`<Contents><Key>dir/literal%20key.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"literal"</ETag><Size>4</Size><StorageClass>STANDARD</StorageClass></Contents>` +
			`<Contents><Key>directory.txt</Key><LastModified>2006-01-02T15:04:05.000Z</LastModified><ETag>"sibling"</ETag><Size>7</Size><StorageClass>STANDARD</StorageClass></Contents>` +
			`</ListBucketResult>`))
	}))
	defer server.Close()

	store, err := storage.NewGenericS3(&config.GenericS3Storage{Endpoint: server.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	endpointDirectory := "s3://" + server.Listener.Addr().String() + "/bucket/dir"
	for _, directoryURL := range []string{endpointDirectory, endpointDirectory + "/"} {
		t.Run(directoryURL, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "inputs")
			flat, err := FlattenInputs(context.Background(), []*tes.Input{{Url: directoryURL, Path: destination, Type: tes.Directory}}, store, events.NewTaskWriter("task", 0, events.Noop{}))
			if err != nil {
				t.Fatalf("flatten endpoint-qualified directory: %v", err)
			}
			want := []*tes.Input{
				{Url: endpointDirectory + "/nested/a b#%.txt", Path: filepath.Join(destination, "nested", "a b#%.txt")},
				{Url: endpointDirectory + "/literal%20key.txt", Path: filepath.Join(destination, "literal%20key.txt")},
			}
			if !reflect.DeepEqual(flat, want) {
				t.Fatalf("flattened inputs = %#v, want %#v", flat, want)
			}
			select {
			case prefix := <-requestedPrefixes:
				if prefix != "dir/" {
					t.Fatalf("ListObjects prefix = %q, want %q", prefix, "dir/")
				}
			default:
				t.Fatal("GenericS3 did not issue a ListObjects request")
			}
		})
	}
}
