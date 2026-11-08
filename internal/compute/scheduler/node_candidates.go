package scheduler

import "context"

// Candidates belong to one scheduling pass. Only committed node reads refresh them.
type nodeCandidates struct {
	nodes   []*Node
	indices map[string]int
}

func newNodeCandidates(nodes []*Node) *nodeCandidates {
	indices := make(map[string]int, len(nodes))
	for i, node := range nodes {
		indices[node.Id] = i
	}
	return &nodeCandidates{nodes: nodes, indices: indices}
}
func (s *nodeCandidates) replace(id string, node *Node) {
	if i, ok := s.indices[id]; ok {
		if node == nil {
			node = &Node{Id: id, State: NodeState_DEAD}
		}
		s.nodes[i] = node
	}
}
func (s *Scheduler) refreshCandidates(ctx context.Context, candidates *nodeCandidates, id string) {
	node, err := s.Nodes.GetNode(ctx, &GetNodeRequest{Id: id})
	if err != nil {
		node = nil
	}
	candidates.replace(id, node)
}

// CandidateNodeSource supplies scheduling fields without worker assignment lists.
type CandidateNodeSource interface {
	ListNodeCandidates(context.Context, *ListNodesRequest) (*ListNodesResponse, error)
}

type candidateNodeLister struct{ CandidateNodeSource }

func (s candidateNodeLister) ListNodes(ctx context.Context, req *ListNodesRequest) (*ListNodesResponse, error) {
	return s.ListNodeCandidates(ctx, req)
}

func (s *Scheduler) readNodeCandidates(ctx context.Context) ([]*Node, error) {
	if source, ok := s.Nodes.(CandidateNodeSource); ok {
		return ListAllNodes(ctx, candidateNodeLister{source})
	}
	return ListAllNodes(ctx, s.Nodes)
}
