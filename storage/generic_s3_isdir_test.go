package storage

import (
	"context"
	"net/http"
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
