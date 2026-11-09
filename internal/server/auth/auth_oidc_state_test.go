package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/config"
	"golang.org/x/oauth2"
)

func TestOIDCStateIsBoundToBrowser(t *testing.T) {
	c := &OidcConfig{local: &config.OidcAuth{}, oauth2: &oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://issuer.example/auth", TokenURL: "https://issuer.example/token", AuthStyle: oauth2.AuthStyleInParams}}}
	start := func() (string, *http.Cookie) {
		out := httptest.NewRecorder()
		c.RedirectToLogin(out, httptest.NewRequest(http.MethodGet, "https://funnel.example/login", nil))
		u, err := url.Parse(out.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		cookies := out.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("login set %d state cookies, want one", len(cookies))
		}
		cookie := cookies[0]
		if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
			t.Fatal("state cookie lacks browser protections")
		}
		return u.Query().Get("state"), cookie
	}
	state, cookie := start()
	other, _ := start()
	if state == "" || state == other {
		t.Fatal("each login must receive fresh random state")
	}
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	calls := 0
	http.DefaultTransport = oidcFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"fixture","token_type":"Bearer","expires_in":3600}`)), Request: req}, nil
	})
	for _, tc := range []struct {
		name, state string
		cookie      *http.Cookie
		want        int
	}{
		{"missing cookie", state, nil, http.StatusBadRequest},
		{"different pending login", other, cookie, http.StatusBadRequest},
		{"matching browser", state, cookie, http.StatusTemporaryRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://funnel.example/login?code=fixture&state="+url.QueryEscape(tc.state), nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			out := httptest.NewRecorder()
			c.HandleAuthCode(out, req)
			if out.Code != tc.want {
				t.Fatalf("callback status = %d, want %d", out.Code, tc.want)
			}
			if tc.want == http.StatusTemporaryRedirect {
				cleared := false
				for _, result := range out.Result().Cookies() {
					if result.Name == cookie.Name && result.MaxAge < 0 {
						cleared = true
					}
				}
				if !cleared {
					t.Error("successful callback did not consume browser state")
				}
			}
		})
	}
	if calls != 1 {
		t.Fatalf("token exchange count = %d, want only matching callback", calls)
	}
}

func TestOIDCEchoTokenDisablesCaching(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://funnel.example/login/token", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "fixture"})
	out := httptest.NewRecorder()
	(&OidcConfig{}).EchoTokenHandler(out, req)
	if out.Code != http.StatusOK || out.Body.String() != "fixture" {
		t.Fatal("token response changed")
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Error("token response can be cached")
	}
}
