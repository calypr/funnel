package server

import (
	"reflect"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
)

func TestForbiddenPathPrefixesAreKubernetesOnly(t *testing.T) {
	conf := config.DefaultConfig()
	service := &TaskService{Config: conf}

	conf.Compute = "local"
	if got := service.forbiddenPathPrefixes(); got != nil {
		t.Fatalf("expected local compute to ignore Kubernetes forbidden paths, got %v", got)
	}

	conf.Compute = "kubernetes"
	want := []string{"/dev", "/proc", "/sys", "/run", "/var/run"}
	if got := service.forbiddenPathPrefixes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected Kubernetes forbidden paths %v, got %v", want, got)
	}
}

func TestForbiddenPathPrefixesHandleMissingConfig(t *testing.T) {
	if got := (&TaskService{}).forbiddenPathPrefixes(); got != nil {
		t.Fatalf("expected missing config to produce no forbidden paths, got %v", got)
	}
}
