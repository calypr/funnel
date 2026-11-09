package storage

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGenericS3IsDirTreatsExactKeyAsFile(t *testing.T) {
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		writeTestResponse(t, w, []byte(`<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated>`+
			`<Contents><Key>sample.bam</Key></Contents>`+
			`<Contents><Key>sample.bam.bai</Key></Contents>`+
			`</ListBucketResult>`))
	})
	isDir, err := store.isDir(context.Background(), "bucket", "sample.bam")
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Fatal("isDir reported the exact key sample.bam as a directory because a sibling key sample.bam.bai shares the prefix")
	}
}

func TestGenericS3IsDirRequiresSlashBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want bool
	}{
		{name: "sibling only", keys: []string{"data/file.txt.bak"}},
		{name: "descendant", keys: []string{"data/file.txt/child"}, want: true},
		{name: "missing"},
		{name: "exact key and descendant", keys: []string{"data/file.txt", "data/file.txt/child"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("prefix") != "data/file.txt" {
					t.Errorf("unexpected list request: %s", r.URL)
				}
				var body strings.Builder
				body.WriteString(`<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated>`)
				for _, key := range tc.keys {
					body.WriteString("<Contents><Key>" + key + "</Key></Contents>")
				}
				body.WriteString("</ListBucketResult>")
				w.Header().Set("Content-Type", "application/xml")
				writeTestResponse(t, w, []byte(body.String()))
			})
			got, err := store.isDir(t.Context(), "bucket", "data/file.txt")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("isDir = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenericS3StatBucketRoot(t *testing.T) {
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("prefix") != "" {
			t.Errorf("unexpected bucket-root request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		writeTestResponse(t, w, []byte(`<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated>`+
			`<Contents><Key>child.txt</Key></Contents></ListBucketResult>`))
	})
	obj, err := store.Stat(t.Context(), "s3://bucket/")
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "s3://bucket/" || obj.Name != "" {
		t.Fatalf("bucket root = %+v", obj)
	}
}
