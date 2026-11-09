package storage

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type sourceCheckTransport func(*http.Request) (*http.Response, error)

func (f sourceCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPPutRequiresRegularSource(t *testing.T) {
	called := false
	b := &HTTP{client: &http.Client{Transport: sourceCheckTransport(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}}
	if _, err := b.Put(t.Context(), "https://upload.example/file", t.TempDir()); err == nil {
		t.Fatal("directory accepted for upload")
	}
	if called {
		t.Fatal("nonregular source reached transport")
	}
}

func TestHTTPPutReplayRequiresRegularSource(t *testing.T) {
	file := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	b := &HTTP{client: &http.Client{Transport: sourceCheckTransport(func(req *http.Request) (*http.Response, error) {
		called = true
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(file, 0700); err != nil {
			t.Fatal(err)
		}
		body, err := req.GetBody()
		if body != nil {
			if closeErr := body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if err == nil {
			t.Error("redirect replay accepted a nonregular source")
		}
		return nil, errors.New("recording transport stopped upload")
	})}}
	if _, err := b.Put(t.Context(), "https://upload.example/file", file); err == nil {
		t.Fatal("expected recording transport to stop the upload")
	}
	if !called {
		t.Fatal("upload did not reach recording transport")
	}
}
