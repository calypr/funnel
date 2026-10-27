package scheduler

import "context"

// A snapshot belongs to one scheduling pass. Only committed node reads refresh it.
type nodeSnapshot struct {
	nodes   []*Node
	indices map[string]int
}

func newNodeSnapshot(nodes []*Node) *nodeSnapshot {
	indices := make(map[string]int, len(nodes))
	for i, node := range nodes {
		indices[node.Id] = i
	}
	return &nodeSnapshot{nodes: nodes, indices: indices}
}
func (s *nodeSnapshot) replace(id string, node *Node) {
	if i, ok := s.indices[id]; ok {
		if node == nil {
			node = &Node{Id: id, State: NodeState_DEAD}
		}
		s.nodes[i] = node
	}
}
func (s *Scheduler) refreshSnapshot(ctx context.Context, snapshot *nodeSnapshot, id string) {
	node, err := s.Nodes.GetNode(ctx, &GetNodeRequest{Id: id})
	if err != nil {
		node = nil
	}
	snapshot.replace(id, node)
}
