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

func TestDefaultSchedulingUsesScores(t *testing.T) {
	nodes := []*Node{
		{Id: "first", State: NodeState_ALIVE, Resources: &Resources{Cpus: 10, RamGb: 10, DiskGb: 10}, Available: &Resources{Cpus: 9, RamGb: 9, DiskGb: 9}},
		{Id: "better-fit", State: NodeState_ALIVE, Resources: &Resources{Cpus: 10, RamGb: 10, DiskGb: 10}, Available: &Resources{Cpus: 2, RamGb: 2, DiskGb: 2}},
	}
	offer := DefaultScheduleAlgorithm(&tes.Task{Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}, nodes, nil)
	if offer == nil || offer.Node.Id != "better-fit" {
		t.Fatalf("offer=%v; want better-fit", offer)
	}
}
func TestNilScoreWeightsPreserveScores(t *testing.T) {
	got := (Scores{CPU: 2, RAM: 4}).Weighted(nil)
	if got[CPU] != 2 || got[RAM] != 4 {
		t.Fatalf("weighted=%v", got)
	}
}
func TestExplicitScoreWeightsPreserveZeroDefaults(t *testing.T) {
	got := (Scores{CPU: 2, RAM: 4}).Weighted(map[string]float32{RAM: 2})
	if got[CPU] != 0 || got[RAM] != 8 {
		t.Fatalf("weighted=%v", got)
	}
}
func TestDefaultSchedulingPreservesInputOrderForEqualScores(t *testing.T) {
	nodes := []*Node{
		{Id: "first", State: NodeState_ALIVE, Resources: &Resources{Cpus: 10, RamGb: 10, DiskGb: 10}, Available: &Resources{Cpus: 5, RamGb: 5, DiskGb: 5}},
		{Id: "second", State: NodeState_ALIVE, Resources: &Resources{Cpus: 10, RamGb: 10, DiskGb: 10}, Available: &Resources{Cpus: 5, RamGb: 5, DiskGb: 5}},
	}
	if offer := DefaultScheduleAlgorithm(&tes.Task{}, nodes, nil); offer == nil || offer.Node.Id != "first" {
		t.Fatalf("offer=%v; want first", offer)
	}
}

func TestDefaultScoresNormalizeResourceUnits(t *testing.T) {
	scores := DefaultScores(&Node{Resources: &Resources{Cpus: 10, RamGb: 20}, Available: &Resources{Cpus: 5, RamGb: 10}}, &tes.Task{Resources: &tes.Resources{CpuCores: 1, RamGb: 2}})
	if scores[CPU] != 0.6 || scores[RAM] != 0.6 {
		t.Fatalf("scores=%v; want CPU and RAM fractions 0.6", scores)
	}
}
