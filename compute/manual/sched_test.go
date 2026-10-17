package manual

import (
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
	. "github.com/stretchr/testify/mock"
)

func simpleNode() *scheduler.Node {
	return &scheduler.Node{
		// This ID MUST match the ID set in setup()
		// because the local scheduler is built to have only a single node
		Id: "test-node-id",
		Resources: &scheduler.Resources{
			Cpus:   1.0,
			RamGb:  1.0,
			DiskGb: 1.0,
		},
		Available: &scheduler.Resources{
			Cpus:   1.0,
			RamGb:  1.0,
			DiskGb: 1.0,
		},
		Zone:  "ok-zone",
		State: scheduler.NodeState_ALIVE,
	}
}

func setup(nodes []*scheduler.Node) (*scheduler.MockSchedulerServiceServer, *scheduler.Scheduler) {
	conf := config.Config{}
	mc := new(scheduler.MockSchedulerServiceServer)

	// Mock in test nodes
	mc.On("ListNodes", Anything, Anything, Anything).Return(&scheduler.ListNodesResponse{
		Nodes: nodes,
	}, nil)

	s := &scheduler.Scheduler{
		Conf:  conf.Scheduler,
		Nodes: mc,
	}
	return mc, s
}

func TestNoNodes(t *testing.T) {
	_, s := setup([]*scheduler.Node{})
	j := &tes.Task{}
	o := s.GetOffer(j)
	if o != nil {
		t.Error("Task scheduled on empty nodes")
	}
}

func TestSingleNode(t *testing.T) {
	_, s := setup([]*scheduler.Node{
		simpleNode(),
	})

	j := &tes.Task{}
	o := s.GetOffer(j)
	if o == nil {
		t.Error("Failed to schedule task on single node")
		return
	}
	if o.Node.Id != "test-node-id" {
		t.Error("Scheduled task on unexpected node")
	}
}

// Test that scheduler ignores nodes without the "ALIVE" state
func TestIgnoreNonAliveNodes(t *testing.T) {
	j := &tes.Task{}

	for name, val := range scheduler.NodeState_value {
		w := simpleNode()
		w.State = scheduler.NodeState(val)
		_, s := setup([]*scheduler.Node{w})
		o := s.GetOffer(j)

		if name == "ALIVE" {
			// Testing ALIVE just so I know this test is node as expected
			if o == nil {
				t.Error("Didn't schedule task to alive node")
			}
		} else {
			if o != nil {
				t.Errorf("Scheduled task to non-alive node: %s", name)
				return
			}
		}

	}
}

// Test whether the scheduler correctly filters nodes based on
// cpu, ram, disk, etc.
func TestMatch(t *testing.T) {
	_, s := setup([]*scheduler.Node{
		simpleNode(),
	})

	var o *scheduler.Offer
	var j *tes.Task

	// Helper which sets up Task.Resources struct to non-nil
	blankTask := func() *tes.Task {
		return &tes.Task{Resources: &tes.Resources{}}
	}

	// test CPUs too big
	j = blankTask()
	j.Resources.CpuCores = 2
	o = s.GetOffer(j)
	if o != nil {
		t.Error("Scheduled task to node without enough CPU resources")
	}

	// test RAM too big
	j = blankTask()
	j.Resources.RamGb = 2.0
	o = s.GetOffer(j)
	if o != nil {
		t.Error("Scheduled task to node without enough RAM resources")
	}

	// test disk too big
	j = blankTask()
	j.Resources.DiskGb = 2.0
	o = s.GetOffer(j)
	if o != nil {
		t.Error("Scheduled task to node without enough DiskGb resources")
	}

	// test zones don't match
	j = blankTask()
	j.Resources.Zones = []string{"test-zone"}
	o = s.GetOffer(j)
	if o != nil {
		t.Error("Scheduled task to node out of zone")
	}

	// Now test a task that fits
	j = blankTask()
	j.Resources.CpuCores = 1
	j.Resources.RamGb = 1.0
	j.Resources.DiskGb = 1.0
	j.Resources.Zones = []string{"ok-zone", "not-ok-zone"}
	o = s.GetOffer(j)
	if o == nil {
		t.Error("Didn't schedule task when resources fit")
	}
}
