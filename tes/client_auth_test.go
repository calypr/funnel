package tes

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBearerTransportPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, address, redirect string
		reject                  bool
		wantRequests            int
	}{
		{name: "plaintext rejected", address: "http://remote.invalid", reject: true},
		{name: "https", address: "https://origin.invalid", wantRequests: 1},
		{name: "downgrade rejected", address: "https://origin.invalid", redirect: "http://127.0.0.1/", reject: true, wantRequests: 1},
		{name: "cross host strips token", address: "https://origin.invalid", redirect: "https://other.invalid/", wantRequests: 2},
		{name: "same host retains token", address: "https://origin.invalid", redirect: "https://origin.invalid/redirected", wantRequests: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
			t.Setenv("FUNNEL_SERVER_TOKEN", "test-token")
			client, err := NewClient(tc.address)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			client.Token = "test-token"
			client.client.Transport = credentialTestTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				want := "Bearer test-token"
				if r.URL.Host == "other.invalid" {
					want = ""
				}
				if got := r.Header.Get("Authorization"); got != want {
					t.Errorf("Authorization = %q, want %q", got, want)
				}
				header := make(http.Header)
				code := http.StatusOK
				if requests == 1 && tc.redirect != "" {
					header.Set("Location", tc.redirect)
					code = http.StatusFound
				}
				return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			_, err = client.GetServiceInfo(context.Background(), &GetServiceInfoRequest{})
			if (err != nil) != tc.reject || requests != tc.wantRequests {
				t.Fatalf("err = %v, requests = %d; want rejection %v, requests %d", err, requests, tc.reject, tc.wantRequests)
			}
		})
	}
}
