package storage

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/ohsu-comp-bio/funnel/config"
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
	c := aws.NewConfig().WithCredentials(credentials.AnonymousCredentials).WithMaxRetries(10)
	s, err := session.NewSession(c)
	if err != nil {
		t.Fatal("Error creating amazon S3 backend:", err)
	}

	store := &AmazonS3{
		sess:     s,
		endpoint: "",
	}

	_, err = store.Get(context.Background(),
		"s3://cellxgene-census-public-us-west-2/cell-census/release.json",
		"_test_download/release.json")
	if err != nil {
		t.Error("Error downloading file:", err)
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
