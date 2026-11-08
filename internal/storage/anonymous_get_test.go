package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"
)

// Download a public file from S3 Storage
// e.g. CZ CELLxGENE Discover Census Data → https://registry.opendata.aws/czi-cellxgene-census
func TestGenericS3AnonymousGet(t *testing.T) {
	store, err := NewGenericS3(&config.GenericS3Storage{
		Endpoint: "https://s3.amazonaws.com/",
		Key:      "",
		Secret:   "",
	})
	if err != nil {
		t.Fatal("Error creating generic S3 backend:", err)
	}

	_, err = store.Get(context.Background(),
		"s3://cellxgene-census-public-us-west-2/cell-census/release.json",
		"_test_download/release.json")
	if err != nil {
		t.Error("Error downloading file:", err)
	}
}

func TestAmazonS3AnonymousGet(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Error("anonymous download was signed")
		}
		if r.URL.Path != "/public/release.json" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"fixture"`)
		if r.Method == http.MethodGet {
			writeTestResponse(t, w, []byte("release"))
		}
	}))
	defer server.Close()
	store, err := NewAmazonS3(&config.AmazonS3Storage{AWSConfig: &config.AWSConfig{
		Endpoint:                  server.URL,
		Region:                    "us-east-1",
		DisableAutoCredentialLoad: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	_, err = store.Get(t.Context(), "s3://public/release.json", path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "release" {
		t.Fatalf("download = %q, want release", body)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want HEAD and GET", requests)
	}
}

func TestGoogleStorageAnonymousGet(t *testing.T) {
	svc, err := storage.NewService(context.TODO(), option.WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatal("Error creating GS backend:", err)
	}

	store := &GoogleCloud{svc}

	// Google Cloud Public Datasets:
	// https://cloud.google.com/datasets?hl=en
	//
	// Broad Institute Public Dataset:
	// https://console.cloud.google.com/storage/browser/gcp-public-data--broad-references
	_, err = store.Get(context.Background(),
		"gs://gcp-public-data--broad-references/C.elegans/WBcel235/README.txt",
		"_test_download/README.txt")
	if err != nil {
		t.Error("Error downloading file:", err)
	}
}
