package scheduler

import (
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
)

// UpdateNode helps scheduler database backend update a node when PutNode() is called.
func UpdateNode(ctx context.Context, cli tes.ReadOnlyServer, node, existing *Node) error {
	var tasks []*tes.Task

	// Clean up terminal tasks.
	for _, id := range node.TaskIds {
		task, err := cli.GetTask(ctx, &tes.GetTaskRequest{
			Id:   id,
			View: tes.View_BASIC.String(),
		})
		if err != nil {
			continue
		}
		// If the task isn't in a terminal state, leave it assigned to the node.
		if !tes.TerminalState(task.GetState()) {
			tasks = append(tasks, task)
		}
	}

	// Update available resources.
	node.Available = AvailableResources(tasks, node.Resources)
	// Update last ping time.
	node.LastPing = time.Now().UnixNano()

	// Merge metadata
	meta := map[string]string{}
	for k, v := range existing.Metadata {
		meta[k] = v
	}
	for k, v := range node.Metadata {
		meta[k] = v
	}
	node.Metadata = meta
	return nil
}

// SubtractResources subtracts the resources requested by "task" from
// the node resources "in".
func SubtractResources(t *tes.Task, in *Resources) *Resources {
	out := &Resources{
		Cpus:   in.GetCpus(),
		RamGb:  in.GetRamGb(),
		DiskGb: in.GetDiskGb(),
	}
	tres := t.GetResources()

	// Cpus are represented by an unsigned int, and if we blindly
	// subtract it will rollover to a very large number. So check first.
	rcpus := uint32(tres.GetCpuCores())
	if rcpus >= out.Cpus {
		out.Cpus = 0
	} else {
		out.Cpus -= rcpus
	}

	out.RamGb -= tres.GetRamGb()
	out.DiskGb -= tres.GetDiskGb()

	// Check minimum values.
	if out.Cpus < 0.0 {
		out.Cpus = 0
	}
	if out.RamGb < 0.0 {
		out.RamGb = 0.0
	}
	if out.DiskGb < 0.0 {
		out.DiskGb = 0.0
	}
	return out
}

// AvailableResources calculates available resources given a list of tasks
// and base resources.
func AvailableResources(tasks []*tes.Task, res *Resources) *Resources {
	a := &Resources{
		Cpus:   res.GetCpus(),
		RamGb:  res.GetRamGb(),
		DiskGb: res.GetDiskGb(),
	}
	for _, t := range tasks {
		a = SubtractResources(t, a)
	}
	return a
}

// UpdateNodeState checks whether a node is dead/gone based on the last
// time it pinged.
func UpdateNodeState(nodes []*Node, conf *config.Scheduler) []*Node {
	var updated []*Node
	for _, node := range nodes {
		prevState := node.State

		if node.State == NodeState_GONE {
			updated = append(updated, node)
			continue
		}

		if node.LastPing == 0 {
			// This shouldn't be happening, because nodes should be
			// created with LastPing, but give it the benefit of the doubt
			// and leave it alone.
			continue
		}

		lastPing := time.Unix(0, node.LastPing)
		d := time.Since(lastPing)

		if node.State == NodeState_UNINITIALIZED || node.State == NodeState_INITIALIZING {

			// The node is initializing, which has a more liberal timeout.
			if d > time.Duration(conf.NodeInitTimeout.GetDuration().AsDuration()) {
				// Looks like the node failed to initialize. Mark it dead
				node.State = NodeState_DEAD
			}

		} else if node.State == NodeState_DEAD && d > time.Duration(conf.NodeDeadTimeout.GetDuration().AsDuration()) {
			// The node has been dead for long enough.
			node.State = NodeState_GONE

		} else if d > time.Duration(conf.NodePingTimeout.GetDuration().AsDuration()) {
			// The node hasn't pinged in awhile, mark it dead.
			node.State = NodeState_DEAD
		}

		if prevState != node.State {
			updated = append(updated, node)
		}
	}
	return updated
}
