package scheduler

import (
	"github.com/calypr/funnel/tes"
)

// DefaultPredicates is a list of Predicate functions that check
// the whether a task fits a node.
var DefaultPredicates = []Predicate{
	ResourcesFit,
	ZonesFit,
	NotDead,
	Alive,
}

// DefaultScheduleAlgorithm implements a simple scheduling algorithm
// that is (currently) common across a few scheduler backends.
// Given a task, list of nodes, and weights, it returns the best Offer or nil.
func DefaultScheduleAlgorithm(j *tes.Task, nodes []*Node, weights map[string]float32) *Offer {

	var best *Offer
	var bestAverage float32
	for _, node := range nodes {
		if !Match(node, j, DefaultPredicates) {
			continue
		}
		scores := DefaultScores(node, j).Weighted(weights)
		average := scores.Average()
		if best == nil || average < bestAverage {
			best = NewOffer(node, j, scores)
			bestAverage = average
		}
	}
	return best
}
