package storage

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// fakeS3ListServer answers ListObjectsV2 requests for a single bucket with the
// given object keys, filtered by the requested prefix like S3 does.
func fakeS3ListServer(t *testing.T, keys []string) *httptest.Server {
	t.Helper()

	type content struct {
		Key  string
		Size int64
	}
	type listBucketResult struct {
		XMLName     xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
		Name        string
		Prefix      string
		KeyCount    int
		MaxKeys     int
		IsTruncated bool
		Contents    []content
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			http.Error(w, "unexpected request: "+r.Method+" "+r.URL.String(), http.StatusNotImplemented)
			return
		}

		prefix := r.URL.Query().Get("prefix")
		result := listBucketResult{Name: "bucket", Prefix: prefix, MaxKeys: 1000}
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) {
				result.Contents = append(result.Contents, content{Key: k, Size: 1})
			}
		}
		result.KeyCount = len(result.Contents)

		w.Header().Set("Content-Type", "application/xml")
		if err := xml.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("encoding ListObjectsV2 response: %v", err)
		}
	}))
}

func TestGenericS3IsDir(t *testing.T) {
	srv := fakeS3ListServer(t, []string{
		"data/file.txt",
		"data/file.txt.bak",
		"data/dir/a.txt",
		"data/dir/b.txt",
		"data/dir-sibling.txt",
	})
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4("key", "secret", ""),
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		object string
		want   bool
	}{
		// A key sharing the name as a prefix ("file.txt.bak") does not make
		// "file.txt" a directory.
		{"data/file.txt", false},
		{"data/dir", true},
		{"data/dir/", true},
		{"data/dir/a.txt", false},
		{"data/missing", false},
		{"data", true},
	}

	for _, tt := range tests {
		t.Run(tt.object, func(t *testing.T) {
			got, err := isDir(context.Background(), client, "bucket", tt.object)
			if err != nil {
				t.Fatalf("isDir(%q): %v", tt.object, err)
			}
			if got != tt.want {
				t.Errorf("isDir(%q) = %v, want %v", tt.object, got, tt.want)
			}
		})
	}
}
