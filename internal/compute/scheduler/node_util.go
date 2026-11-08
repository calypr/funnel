package scheduler

import (
	"time"

	"github.com/calypr/funnel/config"
)

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
