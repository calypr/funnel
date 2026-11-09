package events

import (
	"context"
	"testing"

	"github.com/calypr/funnel/api/tes"
)

func TestTaskBuilderRejectsUnsupportedEntryAndRecoversLegacyState(t *testing.T) {
	for _, unsupported := range []tes.State{tes.State_PREEMPTED, tes.State_CANCELING} {
		builder := &TaskBuilder{Task: &tes.Task{Id: "task", State: tes.State_QUEUED}}
		if err := builder.WriteEvent(context.Background(), NewState("task", unsupported)); err == nil {
			t.Errorf("QUEUED -> %s succeeded; want rejection", unsupported)
		}
		if builder.Task.State != tes.State_QUEUED {
			t.Errorf("state after rejected QUEUED -> %s = %s; want QUEUED", unsupported, builder.Task.State)
		}

		builder = &TaskBuilder{Task: &tes.Task{Id: "legacy", State: unsupported}}
		if err := builder.WriteEvent(context.Background(), NewState("legacy", tes.State_CANCELED)); err != nil {
			t.Errorf("legacy %s -> CANCELED: %v", unsupported, err)
		}
		if builder.Task.State != tes.State_CANCELED {
			t.Errorf("legacy state after cancellation = %s; want CANCELED", builder.Task.State)
		}
	}
}
