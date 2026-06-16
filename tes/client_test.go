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
