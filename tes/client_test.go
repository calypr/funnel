package tes

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/context"
)

func testServer(mux http.Handler) *httptest.Server {
	// Start test server
	lis, err := net.Listen("tcp", ":20001")
	if err != nil {
		panic(err)
	}
	ts := httptest.NewUnstartedServer(mux)
	ts.Listener = lis
	ts.Start()
	return ts
}

func TestAddressTrailingSlash(t *testing.T) {
	c, err := NewClient("http://funnel.com:8000/")
	if err != nil {
		t.Fatal(err)
	}
	if c.address != "http://funnel.com:8000" {
		t.Error("Expected trailing slash to be stripped")
	}
}

func TestGetTask(t *testing.T) {
	var err error

	// Set up test server response
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks/test-id", func(w http.ResponseWriter, r *http.Request) {
		ta := Task{Id: "test-id"}
		o, _ := Marshaler.Marshal(&ta)
		_, err = w.Write(o)
		if err != nil {
			t.Fatal(err)
		}
	})

	ts := testServer(mux)
	defer ts.Close()

	// Make test client call
	c, err := NewClient("http://localhost:20001")
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.GetTask(context.Background(), &GetTaskRequest{
		Id:   "test-id",
		View: View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if body.Id != "test-id" {
		t.Errorf("Unexpected response: %#v", body)
	}
}

func TestGetTaskTrailingSlash(t *testing.T) {
	var err error

	// Set up test server response
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks/test-id", func(w http.ResponseWriter, r *http.Request) {
		ta := Task{Id: "test-id"}
		o, _ := Marshaler.Marshal(&ta)
		_, err = w.Write(o)
		if err != nil {
			t.Fatal(err)
		}
	})

	ts := testServer(mux)
	defer ts.Close()

	// Make test client call
	c, err := NewClient("http://localhost:20001")
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.GetTask(context.Background(), &GetTaskRequest{
		Id:   "test-id",
		View: View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if body.Id != "test-id" {
		t.Errorf("Unexpected response: %#v", body)
	}
}

// TestCancelTaskNoContent verifies the client handles a 204 No Content cancel
// response (empty body) without trying to unmarshal it. See issue #89 / the 204
// cancel-response change.
func TestCancelTaskNoContent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks/test-id:cancel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	ts := testServer(mux)
	defer ts.Close()

	c, err := NewClient("http://localhost:20001")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.CancelTask(context.Background(), &CancelTaskRequest{Id: "test-id"})
	if err != nil {
		t.Fatalf("unexpected error on 204 cancel: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil CancelTaskResponse")
	}
}

func TestClientTimeout(t *testing.T) {
	// Set up test server response
	mux := http.NewServeMux()
	ch := make(chan struct{})
	mux.HandleFunc("/v1/tasks/test-id", func(w http.ResponseWriter, r *http.Request) {
		<-ch
	})

	ts := testServer(mux)
	defer ts.Close()

	c, err := NewClient("http://localhost:20001")
	if err != nil {
		t.Fatal(err)
	}
	c.client.Timeout = 1 * time.Second

	_, err = c.GetTask(context.Background(), &GetTaskRequest{
		Id:   "test-id",
		View: View_MINIMAL.String(),
	})
	close(ch)
	if err == nil {
		t.Fatal("Request did not timeout.")
	}
}
