package gateway

import (
	"encoding/json"
	"testing"

	"github.com/calypr/funnel/internal/compute/scheduler"
)

func TestNodeListResourceJSONFields(t *testing.T) {
	data, err := NewMarshaler().Marshal(&scheduler.ListNodesResponse{Nodes: []*scheduler.Node{{
		Id: "node", Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 16},
		Available: &scheduler.Resources{Cpus: 2, RamGb: 3, DiskGb: 5},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Nodes []struct {
			Resources map[string]float64 `json:"resources"`
			Available map[string]float64 `json:"available"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Nodes) != 1 {
		t.Fatalf("nodes=%d, want 1", len(payload.Nodes))
	}
	actual := map[string]map[string]float64{"resources": payload.Nodes[0].Resources, "available": payload.Nodes[0].Available}
	for kind, fields := range map[string]map[string]float64{
		"resources": {"cpus": 4, "ram_gb": 8, "disk_gb": 16},
		"available": {"cpus": 2, "ram_gb": 3, "disk_gb": 5},
	} {
		for name, want := range fields {
			if got := actual[kind][name]; got != want {
				t.Errorf("%s.%s=%v, want %v", kind, name, got, want)
			}
		}
	}
}
