package server

import (
	"context"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/version"
)

func TestServiceInfoReportsVersionAndOrganization(t *testing.T) {
	previous := version.Version
	version.Version = "v-test"
	t.Cleanup(func() { version.Version = previous })
	service := &TaskService{Name: "Funnel"}
	info, err := service.GetServiceInfo(context.Background(), &tes.GetServiceInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "v-test" {
		t.Fatalf("service version = %q, want v-test", info.Version)
	}
	if info.Type == nil || info.Type.Version != "1.1.0" {
		t.Fatalf("service type = %+v, want TES version 1.1.0", info.Type)
	}
	if info.Organization["name"] != "CALYPR Data Platform" || info.Organization["url"] != "https://github.com/calypr/" {
		t.Fatalf("organization = %v", info.Organization)
	}
}
