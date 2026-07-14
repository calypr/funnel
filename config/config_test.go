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
