package tes_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tesclient "github.com/calypr/funnel/client/tes"
	"github.com/calypr/funnel/tes"
)

func TestRESTClientPreservesPrefixAndEscapesTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/prefix/v1/tasks/a%2Fb%3Fc" {
			t.Errorf("request path = %q", r.URL.EscapedPath())
		}
		if r.URL.Query().Get("view") != "FULL&tag_key=x" {
			t.Errorf("view escaped incorrectly: %q", r.URL.RawQuery)
		}
		if _, err := io.WriteString(w, `{"id":"a/b?c"}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c, err := tesclient.NewClient(server.URL+"/prefix/", tesclient.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.GetTask(t.Context(), &tes.GetTaskRequest{Id: "a/b?c", View: "FULL&tag_key=x"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Id != "a/b?c" {
		t.Fatalf("task = %v", task)
	}
}

func TestRESTClientIgnoresProcessCredentials(t *testing.T) {
	t.Setenv("FUNNEL_SERVER_USER", "implicit-user")
	t.Setenv("FUNNEL_SERVER_PASSWORD", "implicit-password")
	t.Setenv("FUNNEL_SERVER_TOKEN", "implicit-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("library read process credentials")
		}
		if _, err := io.WriteString(w, `{}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c, err := tesclient.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetServiceInfo(t.Context(), &tes.GetServiceInfoRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRESTClientCreateDelegatesValidationToServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/tes/v1/tasks" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"accepted-by-another-tes"}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c, err := tesclient.NewClient(server.URL + "/tes")
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.CreateTask(t.Context(), &tes.Task{Name: "server owns validation"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Id != "accepted-by-another-tes" {
		t.Fatalf("result = %v", result)
	}
}

func TestWaitForTaskHonorsCancellationBeforePolling(t *testing.T) {
	c, err := tesclient.NewClient("https://unused.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() { done <- c.WaitForTask(ctx, "task") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not stop")
	}
}

func TestRESTClientPagingCancelAndServiceInfoUnderPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{}`
		switch r.URL.EscapedPath() {
		case "/prefix/v1/tasks":
			if r.Method != "GET" || r.URL.Query().Get("page_token") != "opaque+/=&" || r.URL.Query().Get("page_size") != "7" {
				t.Errorf("list request = %s %s", r.Method, r.URL)
			}
			body = `{"tasks":[{"id":"listed"}],"next_page_token":"next+/="}`
		case "/prefix/v1/tasks/a%2Fb:cancel":
			if r.Method != "POST" {
				t.Errorf("cancel method = %s", r.Method)
			}
		case "/prefix/v1/service-info":
			if r.Method != "GET" {
				t.Errorf("info method = %s", r.Method)
			}
			body = `{"name":"another TES"}`
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c, err := tesclient.NewClient(server.URL + "/prefix")
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.ListTasks(t.Context(), &tes.ListTasksRequest{PageSize: 7, PageToken: "opaque+/=&"})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPageToken != "next+/=" || len(page.Tasks) != 1 || page.Tasks[0].Id != "listed" {
		t.Fatalf("page = %v", page)
	}
	if _, err := c.CancelTask(t.Context(), &tes.CancelTaskRequest{Id: "a/b"}); err != nil {
		t.Fatal(err)
	}
	info, err := c.GetServiceInfo(t.Context(), &tes.GetServiceInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "another TES" {
		t.Fatal(info)
	}
}
