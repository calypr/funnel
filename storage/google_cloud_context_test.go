package storage

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/api/option"
	gcsapi "google.golang.org/api/storage/v1"
)

type uploadContextTransport struct {
	cancel         context.CancelFunc
	requestContext context.Context
}

func (r *uploadContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	r.requestContext = req.Context()
	r.cancel()
	return nil, errors.New("upload stopped by test transport")
}

func TestGoogleCloudPutPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := &uploadContextTransport{cancel: cancel}
	api, err := gcsapi.NewService(t.Context(), option.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = (&GoogleCloud{svc: api}).Put(ctx, "gs://test-bucket/fixture.txt", file)
	if err == nil {
		t.Fatal("expected upload error")
	}
	if transport.requestContext == nil {
		t.Fatal("upload request was not observed")
	}
	if !errors.Is(transport.requestContext.Err(), context.Canceled) {
		t.Fatalf("upload context error = %v, want cancellation", transport.requestContext.Err())
	}
}
