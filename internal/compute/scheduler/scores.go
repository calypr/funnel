package scheduler

import (
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
// by the given weights. Nil weights preserve the scores; missing explicit weights are zero.
func (s Scores) Weighted(w map[string]float32) Scores {
	out := Scores{}
	for k, v := range s {
		weight := float32(1)
		if w != nil {
			weight = w[k]
		}
		out[k] = v * weight
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
	s[RAM] = float32((avail.GetRamGb() + req.GetRamGb()) / tot.GetRamGb())
	return s
}
