package scheduler

import (
	"github.com/calypr/funnel/tes"
)

// Offer describes a node offered by a scheduler for a task.
// The Scores describe how well the task fits this node,
// which could be used by other a scheduler to pick the best offer.
type Offer struct {
	TaskID string
	Node   *Node
	Scores Scores
}

// NewOffer returns a new Offer instance.
func NewOffer(n *Node, t *tes.Task, s Scores) *Offer {
	return &Offer{
		TaskID: t.Id,
		Node:   n,
		Scores: s,
	}
}
