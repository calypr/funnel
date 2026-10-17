package scheduler

import (
	"runtime/debug"
	"testing"

	"github.com/calypr/funnel/tes"
)

func TestDefaultScoresEmptyTask(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("DefaultScores panic on empty task/node\n%s", debug.Stack())
		}
	}()

	j := &tes.Task{}
	w := &Node{}
	DefaultScores(w, j)
}
