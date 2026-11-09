package tes_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/funnel/api/tes"
	tesclient "github.com/calypr/funnel/client/tes"
)

func TestRESTClientEnvironmentCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, token, user, password, want string
	}{
		{name: "bearer", token: "test-token", want: "Bearer test-token"},
		{name: "bearer takes precedence", token: "test-token", user: "u", password: "p", want: "Bearer test-token"},
		{name: "basic", user: "u", password: "p", want: "Basic dTpw"},
		{name: "empty basic password preserved", user: "u", want: "Basic dTo="},
		{name: "empty basic username preserved", password: "p", want: "Basic OnA="},
		{name: "anonymous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
			t.Setenv("FUNNEL_SERVER_TOKEN", tc.token)
			t.Setenv("FUNNEL_SERVER_USER", tc.user)
			t.Setenv("FUNNEL_SERVER_PASSWORD", tc.password)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Values("Authorization"); (tc.want == "" && len(got) != 0) || (tc.want != "" && (len(got) != 1 || got[0] != tc.want)) {
					t.Errorf("Authorization = %q, want %q", got, tc.want)
				}
				if _, err := io.WriteString(w, `{}`); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			client, err := tesclient.NewClient(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.GetServiceInfo(t.Context(), &tes.GetServiceInfoRequest{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
