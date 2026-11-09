package termdash

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calypr/funnel/tes"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCancelKeyRequestsConfirmation(t *testing.T) {
	d := &dashboard{width: 100, height: 20, tasks: []*tes.Task{{Id: "task-1"}}, selected: "task-1"}
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !strings.Contains(d.View(), "task-1") || !strings.Contains(strings.ToLower(d.View()), "cancel") {
		t.Fatal("cancel key did not show confirmation for the selected task")
	}
}

func TestDashboardNodesAndCancellation(t *testing.T) {
	t.Setenv("FUNNEL_SERVER_USER", "operator")
	t.Setenv("FUNNEL_SERVER_PASSWORD", "secret")
	var canceled atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "operator" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body string
		switch r.URL.Path {
		case "/v1/nodes":
			if r.URL.Query().Get("page_token") == "next" {
				body = `{"nodes":[{"id":"node-2","state":"DEAD"}]}`
			} else {
				body = `{"nodes":[{"id":"node-1","state":"ALIVE"}],"next_page_token":"next"}`
			}
		case "/v1/tasks/task-1:cancel":
			if r.Method != http.MethodPost {
				t.Errorf("cancel method = %s", r.Method)
			}
			canceled.Add(1)
			body = `{}`
		case "/v1/tasks":
			body = `{"tasks":[]}`
		default:
			http.NotFound(w, r)
			return
		}
		_, err := fmt.Fprint(w, body)
		if err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer server.Close()

	source, err := NewTaskSource(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard{source: source, width: 100, height: 20, tasks: []*tes.Task{{Id: "task-1"}}, selected: "task-1"}
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	d.Update(cmd())
	if !strings.Contains(d.View(), "node-1") || !strings.Contains(d.View(), "node-2") {
		t.Fatalf("worker list: %s", d.View())
	}
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(d.View(), "node-1") || !strings.Contains(d.View(), "ALIVE") {
		t.Fatalf("worker details: %s", d.View())
	}
	d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if canceled.Load() != 0 {
		t.Fatal("task canceled before confirmation")
	}
	_, cmd = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd != nil || canceled.Load() != 0 {
		t.Fatal("declining confirmation canceled the task")
	}
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	_, cmd = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	d.Update(cmd())
	if canceled.Load() != 1 {
		t.Fatalf("cancel requests = %d", canceled.Load())
	}
}

func TestNodesReportsUnavailableScheduler(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	source, err := NewTaskSource(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Nodes()
	if err == nil || !strings.Contains(err.Error(), "scheduler node API is unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestCancelFailureIsShown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cancellation denied", http.StatusForbidden)
	}))
	defer server.Close()
	source, err := NewTaskSource(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard{source: source, width: 100, height: 20, tasks: []*tes.Task{{Id: "task-1"}}, selected: "task-1", showHeader: true}
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	d.Update(cmd())
	if !strings.Contains(d.View(), "cancellation denied") {
		t.Fatalf("dashboard did not show cancellation failure: %s", d.View())
	}
}

func TestWorkerKeyRequestsNodeList(t *testing.T) {
	d := &dashboard{width: 100, height: 20}
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if cmd == nil {
		t.Fatal("worker key did not request the node list")
	}
}

func TestRecordedWorkersUseTaskMetadataAcrossPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Query().Get("state") {
		case "RUNNING":
			if r.URL.Query().Get("page_token") == "page-2" {
				body = `{"tasks":[{"id":"k8s-task","state":"RUNNING","logs":[{"metadata":{"hostname":"worker-pod"}}]}]}`
			} else {
				body = `{"tasks":[{"id":"aws-task","state":"RUNNING","logs":[{"metadata":{"hostname":"batch-host","awsbatch_id":"batch-job"}}]}],"next_page_token":"page-2"}`
			}
		case "INITIALIZING":
			body = `{"tasks":[{"id":"pending","state":"INITIALIZING"}]}`
		default:
			t.Errorf("unexpected state %q", r.URL.Query().Get("state"))
			return
		}
		_, err := fmt.Fprint(w, body)
		if err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer server.Close()
	source, err := NewTaskSource(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	workers, err := source.TaskWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 2 || workers[0].TaskID != "aws-task" || workers[0].Hostname != "batch-host" || workers[0].Metadata["awsbatch_id"] != "batch-job" || workers[1].TaskID != "k8s-task" || workers[1].Hostname != "worker-pod" {
		t.Fatalf("recorded task workers = %+v", workers)
	}
}

func TestWorkerViewShowsRecordedWorkersWithoutScheduler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			http.NotFound(w, r)
			return
		}
		body := `{"tasks":[]}`
		if r.URL.Query().Get("state") == "RUNNING" {
			body = `{"tasks":[{"id":"cloud-task","state":"RUNNING","logs":[{"metadata":{"hostname":"cloud-host","awsbatch_id":"job-123"}}]}]}`
		}
		_, err := fmt.Fprint(w, body)
		if err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer server.Close()
	source, err := NewTaskSource(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard{source: source, width: 120, height: 20}
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	d.Update(cmd())
	if !strings.Contains(d.View(), "cloud-host") || !strings.Contains(d.View(), "recorded") {
		t.Fatalf("recorded cloud worker not shown: %s", d.View())
	}
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(d.View(), "job-123") {
		t.Fatalf("worker details missing job metadata: %s", d.View())
	}
}
