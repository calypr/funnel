package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type signalingWriter struct {
	bytes.Buffer
	once    sync.Once
	written chan struct{}
}

func (w *signalingWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(w.Buffer.String(), "task-1") {
		w.once.Do(func() { close(w.written) })
	}
	return n, err
}

func TestListAllStreamsPages(t *testing.T) {
	output := &signalingWriter{written: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page_token") {
		case "":
			_, err := fmt.Fprint(w, `{"tasks":[{"id":"task-1","state":"RUNNING"}],"nextPageToken":"page-2"}`)
			if err != nil {
				t.Error(err)
			}
		case "page-2":
			select {
			case <-output.written:
			case <-time.After(2 * time.Second):
				t.Error("first task was not written before requesting page two")
			}
			_, err := fmt.Fprint(w, `{"tasks":[],"nextPageToken":"page-3"}`)
			if err != nil {
				t.Error(err)
			}
		case "page-3":
			_, err := fmt.Fprint(w, `{"tasks":[{"id":"task-2","state":"COMPLETE"}]}`)
			if err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected page token %q", r.URL.Query().Get("page_token"))
		}
	}))
	defer server.Close()
	if err := List(server.URL, "basic", "", "", nil, "", 1, true, output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid output %q: %v", output.String(), err)
	}
	if len(result.Tasks) != 2 || result.Tasks[0].ID != "task-1" || result.Tasks[1].ID != "task-2" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListPreservesSinglePageAndEmptyOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "empty" {
			_, err := fmt.Fprint(w, `{}`)
			if err != nil {
				t.Error(err)
			}
			return
		}
		_, err := fmt.Fprint(w, `{"tasks":[{"id":"task-1"}],"nextPageToken":"next"}`)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := List(server.URL, "basic", "", "", nil, "", 10, false, &output); err != nil {
		t.Fatal(err)
	}
	var single map[string]any
	if err := json.Unmarshal(output.Bytes(), &single); err != nil || single["nextPageToken"] != "next" {
		t.Fatalf("single page = %s, error = %v", output.String(), err)
	}
	output.Reset()
	if err := List(server.URL, "basic", "empty", "", nil, "", 10, true, &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "{}" {
		t.Fatalf("empty result = %s", output.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestListReportsOutputErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := fmt.Fprint(w, `{"tasks":[{"id":"task-1"}]}`)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	for _, all := range []bool{false, true} {
		if err := List(server.URL, "basic", "", "", nil, "", 1, all, failingWriter{}); err == nil || !strings.Contains(err.Error(), "output failed") {
			t.Fatalf("all=%t error=%v", all, err)
		}
	}
}

func TestListAllReportsLaterPageFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") != "" {
			http.Error(w, "page failed", http.StatusServiceUnavailable)
			return
		}
		_, err := fmt.Fprint(w, `{"tasks":[{"id":"task-1"}],"nextPageToken":"next"}`)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	err := List(server.URL, "basic", "", "", nil, "", 1, true, &output)
	if err == nil || !strings.Contains(err.Error(), "page failed") {
		t.Fatalf("expected page error, got %v", err)
	}
	if !strings.Contains(output.String(), "task-1") || json.Valid(output.Bytes()) {
		t.Fatalf("expected partial output after a failed page, got %q", output.String())
	}
}

func TestListEmptyTagValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tag_key") != "foo" {
			t.Errorf("query = %v, want tag_key=foo", q)
		}
		values, ok := q["tag_value"]
		if !ok || len(values) != 1 || values[0] != "" {
			t.Errorf("query = %v, want empty tag_value", q)
		}
		_, err := fmt.Fprint(w, `{}`)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	if err := List(server.URL, "basic", "", "", []string{"foo="}, "", 10, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
