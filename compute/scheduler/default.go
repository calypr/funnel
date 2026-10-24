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

	offers := []*Offer{}
	for _, n := range nodes {
		// Filter out nodes that don't match the task request.
		// Checks CPU, RAM, disk space, etc.
		if !Match(n, j, DefaultPredicates) {
			continue
		}

		sc := DefaultScores(n, j)
		sc = sc.Weighted(weights)

		offer := NewOffer(n, j, sc)
		offers = append(offers, offer)
	}

	// No matching nodes were found.
	if len(offers) == 0 {
		return nil
	}

	SortByAverageScore(offers)
	return offers[0]
}
