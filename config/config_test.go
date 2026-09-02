package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestNodeResourceConfigParsing(t *testing.T) {
	yaml := `
Node:
  Resources:
    Cpus: 42
    RamGb: 2.5
    DiskGb: 50.0
`
	conf := Config{}
	Parse([]byte(yaml), &conf)

	if conf.Node.Resources.Cpus != 42 {
		t.Fatal("unexpected cpus")
	}
	if conf.Node.Resources.RamGb != 2.5 {
		t.Fatal("unexpected ram")
	}
	if conf.Node.Resources.DiskGb != 50.0 {
		t.Fatal("unexpected disk")
	}
}

func TestConfigParsing(t *testing.T) {
	conf := EmptyConfig()
	err := ParseFile("./default-config.yaml", conf)
	if err != nil {
		t.Error("unexpected error:", err)
	}
	if got, want := conf.Kubernetes.ForbiddenPathPrefixes, []string{"/dev", "/proc", "/sys", "/run", "/var/run"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected default forbidden paths %v, got %v", want, got)
	}

	yaml := `
BadKey: foo
Node:
  Resources:
    Cpus: 42
    RamGb: 2.5
    DiskGb: 50.0
`
	conf = &Config{}
	err = Parse([]byte(yaml), conf)
	if err == nil {
		t.Error("expected error")
	}
}

func TestEmbeddedDefaultConfigForbiddenPaths(t *testing.T) {
	raw, ok := Examples()["default-config"]
	if !ok {
		t.Fatal("embedded default-config example is missing")
	}

	conf := EmptyConfig()
	if err := Parse([]byte(raw), conf); err != nil {
		t.Fatal("parsing embedded default-config example:", err)
	}

	want := []string{"/dev", "/proc", "/sys", "/run", "/var/run"}
	if got := conf.Kubernetes.ForbiddenPathPrefixes; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected embedded forbidden paths %v, got %v", want, got)
	}
}

func TestRPCClientCredentialParsing(t *testing.T) {
	conf := EmptyConfig()
	raw := []byte(`
RPCClient:
  Credential:
    User: funnel
    Password: abc123
`)
	if err := Parse(raw, conf); err != nil {
		t.Fatal("parsing nested RPC credential:", err)
	}
	if got := conf.RPCClient.Credential; got.User != "funnel" || got.Password != "abc123" {
		t.Fatalf("unexpected RPC credential: %+v", got)
	}
}

func TestRPCClientLegacyCredentialFieldsRejected(t *testing.T) {
	conf := EmptyConfig()
	raw := []byte(`
RPCClient:
  User: funnel
  Password: abc123
`)
	err := Parse(raw, conf)
	if err == nil {
		t.Fatal("expected legacy RPC credential fields to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown field "User"`) {
		t.Fatalf("expected unknown User field error, got %v", err)
	}
}
