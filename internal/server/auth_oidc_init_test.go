package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/config"
)

type oidcFixtureTransport func(*http.Request) (*http.Response, error)

func (f oidcFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestInitOIDCConfig(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = oidcFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"issuer":"https://issuer.example","jwks_uri":"https://issuer.example/jwks","authorization_endpoint":"https://issuer.example/auth","token_endpoint":"https://issuer.example/token"}`
		if req.URL.Path == "/jwks" {
			body = `{"keys":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	defer func() { http.DefaultTransport = previous }()
	c := &OidcConfig{local: &config.OidcAuth{ServiceConfigURL: "https://issuer.example/config", ClientId: "funnel", ClientSecret: "fixture", RedirectURL: "https://funnel.example/login", Admins: []string{"alice"}}}
	defer func() {
		if c.jwks != nil {
			if err := c.jwks.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		}
	}()
	c.initConfig()
	if c.oauth2 == nil || c.oauth2.ClientID != "funnel" || c.oauth2.Endpoint.TokenURL != "https://issuer.example/token" || !c.admins["alice"] {
		t.Fatal("OIDC initialization did not populate the client and administrator configuration")
	}
}
