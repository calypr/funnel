package config

import (
	"reflect"
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
