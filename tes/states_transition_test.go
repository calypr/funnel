package tes

import "testing"

func TestValidateTransitionUnsupportedStatesAndLegacyRecovery(t *testing.T) {
	unsupported := []State{State_PREEMPTED, State_CANCELING}
	for _, target := range unsupported {
		for _, from := range []State{State_UNKNOWN, State_QUEUED, State_INITIALIZING} {
			if err := ValidateTransition(from, target); err == nil {
				t.Errorf("ValidateTransition(%s, %s) succeeded; want rejection", from, target)
			}
		}
		if err := ValidateTransition(target, target); err != nil {
			t.Errorf("ValidateTransition(%s, %s) = %v; want idempotent success", target, target, err)
		}
		for _, targetState := range []State{State_CANCELED, State_SYSTEM_ERROR} {
			if err := ValidateTransition(target, targetState); err != nil {
				t.Errorf("ValidateTransition(%s, %s) = %v; want legacy recovery", target, targetState, err)
			}
		}
		for _, targetState := range []State{State_QUEUED, State_INITIALIZING, State_COMPLETE} {
			if err := ValidateTransition(target, targetState); err == nil {
				t.Errorf("ValidateTransition(%s, %s) succeeded; want rejection", target, targetState)
			}
		}
	}
	for _, from := range []State{State_COMPLETE, State_CANCELED} {
		if err := ValidateTransition(from, State_SYSTEM_ERROR); err == nil {
			t.Errorf("ValidateTransition(%s, SYSTEM_ERROR) succeeded; want terminal irreversibility", from)
		}
	}
}
