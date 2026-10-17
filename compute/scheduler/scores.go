package scheduler

import (
	"sort"

	"github.com/calypr/funnel/tes"
)

// Scores describe how well a task fits a node.
type Scores map[string]float32

// Scores keys
const (
	CPU = "cpu"
	RAM = "ram"
)

// Average returns the average of the scores.
func (s Scores) Average() float32 {
	var tot float32
	for _, v := range s {
		tot += v
	}
	return tot / float32(len(s))
}

// Weighted returns a new Scores instance with each score multiplied
// by the given weights. Weights default to 0.0
func (s Scores) Weighted(w map[string]float32) Scores {
	out := Scores{}
	for k, v := range s {
		out[k] = v * w[k]
	}
	return out
}

// DefaultScores returns a default set of scores.
func DefaultScores(w *Node, t *tes.Task) Scores {
	req := t.GetResources()
	tot := w.GetResources()
	avail := w.GetAvailable()
	s := Scores{}

	s[CPU] = float32(int32(avail.GetCpus())+req.GetCpuCores()) / float32(tot.GetCpus())
	s[RAM] = float32(avail.GetRamGb() + req.GetRamGb()/tot.GetRamGb())
	return s
}

// SortByAverageScore sorts the given offers by their average score.
// This modifies the offers list in place.
func SortByAverageScore(offers []*Offer) {
	// Pre-calculate the averages scores so that we're not re-calculating
	// many times during sort
	averages := make([]float32, len(offers))
	for _, o := range offers {
		averages = append(averages, o.Scores.Average())
	}
	s := sorter{offers, averages}
	sort.Sort(s)
}

// sorter is a helper which implements Go's sort.Interface
// in order to sort Offers by average score
type sorter struct {
	offers   []*Offer
	averages []float32
}

func (s sorter) Len() int {
	return len(s.offers)
}
func (s sorter) Swap(i, j int) {
	s.offers[i], s.offers[j] = s.offers[j], s.offers[i]
	s.averages[i], s.averages[j] = s.averages[j], s.averages[i]
}
func (s sorter) Less(i, j int) bool {
	return s.averages[i] < s.averages[j]
}
