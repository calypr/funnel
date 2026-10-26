package task

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateReportsJSONErrorLocation(t *testing.T) {
	cases := []struct {
		name, input, location string
	}{
		{"syntax", "{\n  \"name\": }", "line 2, column 11"},
		{"truncated", "{\n  \"name\": \"unfinished", "line 2, column 22"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Create("http://127.0.0.1:1", nil, strings.NewReader(tc.input), io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.location) {
				t.Fatalf("expected %q in error, got %v", tc.location, err)
			}
		})
	}
}

func TestCreateReportsJSONErrorLocationFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(path, []byte("{\r\n  \"name\": }"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Create("http://127.0.0.1:1", []string{path}, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "line 2, column 11") {
		t.Fatalf("expected file error location, got %v", err)
	}
}

func TestCreateReportsLocationAfterPreviousTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"first"}`)
	}))
	defer server.Close()

	input := "{\"executors\":[{\"image\":\"alpine\",\"command\":[\"true\"]}]}\n{\n  \"name\": }"
	err := Create(server.URL, nil, strings.NewReader(input), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "line 3, column 11") {
		t.Fatalf("expected location after first task, got %v", err)
	}
}
