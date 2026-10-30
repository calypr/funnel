package tes

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"
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
	// Set up test server response
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks/test-id", func(w http.ResponseWriter, r *http.Request) {
		ta := Task{Id: "test-id"}
		o, err := Marshaler.Marshal(&ta)
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		if _, err := w.Write(o); err != nil {
			t.Errorf("write response: %v", err)
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
	// Set up test server response
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks/test-id", func(w http.ResponseWriter, r *http.Request) {
		ta := Task{Id: "test-id"}
		o, err := Marshaler.Marshal(&ta)
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		if _, err := w.Write(o); err != nil {
			t.Errorf("write response: %v", err)
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

func TestClientReturnsMalformedEndpointError(t *testing.T) {
	c, err := NewClient("http://bad host")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetTask(context.Background(), &GetTaskRequest{Id: "test-id"})
	if err == nil || !strings.Contains(err.Error(), "creating GET request") {
		t.Fatalf("GetTask error = %v, want request construction error", err)
	}
}
