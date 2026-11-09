package tes

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/tes"
)

type credentialTestTransport func(*http.Request) (*http.Response, error)

func (f credentialTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCredentialTransportPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, address, user, password, opt string
		reject                             bool
		explicit                           bool
	}{
		{name: "remote credentials", address: "http://remote.invalid", user: "u", reject: true},
		{name: "password only", address: "http://remote.invalid", password: "p", reject: true},
		{name: "userinfo", address: "http://u:p@remote.invalid", reject: true},
		{name: "unauthenticated", address: "http://remote.invalid"},
		{name: "https", address: "https://remote.invalid", user: "u"},
		{name: "loopback", address: "http://127.0.0.1:8000", user: "u"},
		{name: "ipv6 loopback", address: "http://[::1]:8000", user: "u"},
		{name: "localhost lookalike", address: "http://localhost.invalid", user: "u", reject: true},
		{name: "opt in", address: "http://remote.invalid", user: "u", opt: "true"},
		{name: "explicit option", address: "http://remote.invalid", user: "u", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", tc.opt)
			t.Setenv("FUNNEL_SERVER_USER", "")
			t.Setenv("FUNNEL_SERVER_PASSWORD", "")
			t.Setenv("FUNNEL_SERVER_TOKEN", "")
			var options []ClientOption
			if tc.explicit {
				options = append(options, WithInsecureCredentials())
			}
			c, err := NewClient(tc.address, options...)
			if err != nil {
				t.Fatal(err)
			}
			c.User, c.Password = tc.user, tc.password
			sent := false
			c.client.Transport = credentialTestTransport(func(r *http.Request) (*http.Response, error) {
				sent = true
				if tc.user == "" && tc.password == "" && r.URL.User == nil && r.Header.Get("Authorization") != "" {
					t.Error("empty credentials emitted")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			_, err = c.GetTask(context.Background(), &tes.GetTaskRequest{Id: "test"})
			if (err != nil) != tc.reject {
				t.Fatalf("error=%v, want rejection %v", err, tc.reject)
			}
			if sent == tc.reject {
				t.Fatalf("sent=%v, reject=%v", sent, tc.reject)
			}
		})
	}
}

func TestCredentialRedirectDowngrade(t *testing.T) {
	t.Setenv("FUNNEL_SERVER_TOKEN", "")
	t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
	c, err := NewClient("https://origin.invalid")
	if err != nil {
		t.Fatal(err)
	}
	c.User, c.Password = "u", "p"
	sent := 0
	c.client.Transport = credentialTestTransport(func(r *http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"http://127.0.0.1:8000/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	_, err = c.GetTask(context.Background(), &tes.GetTaskRequest{Id: "test"})
	if err == nil || sent != 1 {
		t.Fatalf("downgrade error=%v, requests sent=%v", err, sent)
	}
}
