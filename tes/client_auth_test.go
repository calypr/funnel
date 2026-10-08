package tes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCredentials(t *testing.T) {
	cases := []struct {
		name   string
		env    map[string]string
		header string
	}{
		{"bearer token", map[string]string{"FUNNEL_SERVER_TOKEN": "jwt"}, "Bearer jwt"},
		{"token wins over basic", map[string]string{"FUNNEL_SERVER_TOKEN": "jwt", "FUNNEL_SERVER_USER": "u", "FUNNEL_SERVER_PASSWORD": "p"}, "Bearer jwt"},
		{"basic", map[string]string{"FUNNEL_SERVER_USER": "u", "FUNNEL_SERVER_PASSWORD": "p"}, "Basic dTpw"},
		{"none", map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"FUNNEL_SERVER_TOKEN", "FUNNEL_SERVER_USER", "FUNNEL_SERVER_PASSWORD"} {
				t.Setenv(k, c.env[k])
			}

			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			client, err := NewClient(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = client.GetServiceInfo(context.Background(), &GetServiceInfoRequest{})
			if got != c.header {
				t.Errorf("Authorization = %q, want %q", got, c.header)
			}
		})
	}
}
