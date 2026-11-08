package taskmodel

import (
	"testing"

	"github.com/calypr/funnel/api/tes"
)

func TestValidateTransitionUnsupportedStatesAndLegacyRecovery(t *testing.T) {
	unsupported := []tes.State{tes.State_PREEMPTED, tes.State_CANCELING}
	for _, target := range unsupported {
		for _, from := range []tes.State{tes.State_UNKNOWN, tes.State_QUEUED, tes.State_INITIALIZING} {
			if err := ValidateTransition(from, target); err == nil {
				t.Errorf("ValidateTransition(%s, %s) succeeded; want rejection", from, target)
			}
		}
		if err := ValidateTransition(target, target); err != nil {
			t.Errorf("ValidateTransition(%s, %s) = %v; want idempotent success", target, target, err)
		}
		for _, targetState := range []tes.State{tes.State_CANCELED, tes.State_SYSTEM_ERROR} {
			if err := ValidateTransition(target, targetState); err != nil {
				t.Errorf("ValidateTransition(%s, %s) = %v; want legacy recovery", target, targetState, err)
			}
		}
		for _, targetState := range []tes.State{tes.State_QUEUED, tes.State_INITIALIZING, tes.State_COMPLETE} {
			if err := ValidateTransition(target, targetState); err == nil {
				t.Errorf("ValidateTransition(%s, %s) succeeded; want rejection", target, targetState)
			}
		}
	}
	for _, from := range []tes.State{tes.State_COMPLETE, tes.State_CANCELED} {
		if err := ValidateTransition(from, tes.State_SYSTEM_ERROR); err == nil {
			t.Errorf("ValidateTransition(%s, SYSTEM_ERROR) succeeded; want terminal irreversibility", from)
		}
	}
}
