package server

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/calypr/funnel/tests"
)

func TestWebdash(t *testing.T) {
	tests.SetLogOutput(log, t)
	// Get the webdash health check endpoint
	address := fun.Conf.Server.HTTPAddress()
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(address + "/health.html")
	if err != nil {
		t.Fatal(err)
	}

	// Get the response body
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			t.Errorf("close webdash response body: %v", err)
		}
	}()
	b, berr := io.ReadAll(resp.Body)
	if berr != nil {
		t.Fatal(berr)
	}

	if string(b) != "OK\n" {
		t.Fatal("Webdash health check fail", string(b))
	}
}
