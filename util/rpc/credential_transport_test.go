package rpc

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/calypr/funnel/config"
	"google.golang.org/grpc"
)

func TestDialRejectsRemoteCredentials(t *testing.T) {
	t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
	conn, err := Dial(context.Background(), &config.RPCClient{ServerAddress: "remote.invalid:9090", Credential: &config.BasicCredential{User: "u", Password: "p"}})
	if conn != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err == nil {
		t.Fatal("remote plaintext credentials accepted")
	}
}

func TestLoopbackCredentialAddresses(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    bool
	}{
		{"localhost:9090", true}, {"127.0.0.1:9090", true}, {"[::1]:9090", true},
		{"localhost.invalid:9090", false}, {"remote.invalid:9090", false}, {"dns:///localhost:9090", false},
	} {
		if loopbackAddress(tc.address) != tc.want {
			t.Errorf("loopbackAddress(%q) != %v", tc.address, tc.want)
		}
	}
}

func TestStandaloneCredentialsRequireSecurity(t *testing.T) {
	t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "")
	if !(&loginCreds{User: "u"}).RequireTransportSecurity() {
		t.Fatal("standalone credentials allow plaintext")
	}
	t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", "true")
	if (&loginCreds{User: "u"}).RequireTransportSecurity() {
		t.Fatal("explicit override ignored")
	}
}

func TestDialPermittedCredentialModes(t *testing.T) {
	for _, tc := range []struct{ name, address, user, opt string }{
		{"loopback", "127.0.0.1:9090", "u", ""},
		{"explicit override", "remote.invalid:9090", "u", "true"},
		{"unauthenticated", "remote.invalid:9090", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FUNNEL_ALLOW_INSECURE_CREDENTIALS", tc.opt)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := Dial(ctx, &config.RPCClient{ServerAddress: tc.address, Credential: &config.BasicCredential{User: tc.user}}, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return nil, errors.New("synthetic dialer: no network")
			}))
			if err != nil {
				t.Fatal(err)
			}
			closeErr := conn.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}
