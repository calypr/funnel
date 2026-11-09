package termdash

import (
	"net/http"
	"testing"
)

type credentialTransportTestFunc func(*http.Request) (*http.Response, error)

func (f credentialTransportTestFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNodesRejectsRemotePlaintextCredentials(t *testing.T) {
	t.Setenv("FUNNEL_SERVER_USER", "user")
	t.Setenv("FUNNEL_SERVER_PASSWORD", "password")
	t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
	source, err := NewTaskSource("http://remote.invalid", 10)
	if err != nil {
		t.Fatal(err)
	}
	sent := false
	source.nodeClient.Transport = credentialTransportTestFunc(func(*http.Request) (*http.Response, error) {
		sent = true
		t.Error("credentials reached remote plaintext transport")
		return nil, nil
	})
	_, err = source.Nodes()
	if err == nil || sent {
		t.Fatalf("Nodes error=%v, sent=%v", err, sent)
	}
}
