package gce

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/calypr/funnel/config"
)

func loadTestData(name string) []byte {
	b, err := os.ReadFile(name + ".json")
	if err != nil {
		panic(err)
	}
	return b
}

func testServer(f http.HandlerFunc) *httptest.Server {
	// Start test server
	lis, err := net.Listen("tcp", ":20002")
	if err != nil {
		panic(err)
	}
	// Set up test server response
	mux := http.NewServeMux()
	mux.HandleFunc("/computeMetadata/v1/", f)
	ts := httptest.NewUnstartedServer(mux)
	ts.Listener = lis
	ts.Start()
	return ts
}

// Tests that the code can correctly get metadata from
// a GCE metadata server and merge it with a config.Config instance.
func TestGetMetadata(t *testing.T) {
	ts := testServer(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.URL.Query()["recursive"]; !ok || v[0] != "true" {
			t.Fatal("Expected recursive query")
		}
		w.Write(loadTestData("metadata1")) //nolint:errcheck // Fixture response is unused once the test client disconnects.
	})
	defer ts.Close()

	var cerr error
	conf := config.DefaultConfig()
	meta, err := LoadMetadataFromURL("http://localhost:20002")
	if err != nil {
		t.Fatal(err)
	}
	conf, cerr = WithMetadataConfig(conf, meta)
	if cerr != nil {
		t.Fatal(cerr)
	}

	// When meta.instance.attributes.funnelNode != ""
	// conf.Scheduler.Node.ID == meta.instance.name
	if conf.Node.ID != "funnel-node-1492486244" {
		t.Fatal("Unexpected node ID")
	}
}
