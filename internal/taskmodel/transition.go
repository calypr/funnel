package taskmodel

import (
	"fmt"

	"github.com/calypr/funnel/tes"
)

// TransitionError describes an invalid state transition.
type TransitionError struct {
	From, To tes.State
}

func (te *TransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s",
		te.From.String(), te.To.String())
}

// ValidateTransition validates a task state transition.
// Returns a TransitionError if the transition is not valid.
func ValidateTransition(from, to tes.State) error {

	if from == to {
		return nil
	}
	if to == tes.State_PREEMPTED || to == tes.State_CANCELING {
		return &TransitionError{from, to}
	}
	if from == tes.State_PREEMPTED || from == tes.State_CANCELING {
		if to == tes.Canceled || to == tes.SystemError {
			return nil
		}
		return &TransitionError{from, to}
	}

	if from == tes.Paused || to == tes.Paused {
		return fmt.Errorf("paused state is not implemented")
	}

	switch from {
	case tes.Unknown:
		return nil

	case tes.Queued:
		if to == tes.Unknown {
			return &TransitionError{from, to}
		}
		return nil

	case tes.Initializing:
		if to == tes.Unknown || to == tes.Queued {
			return &TransitionError{from, to}
		}
		return nil

	case tes.Running:
		if to == tes.Complete || to == tes.ExecutorError || to == tes.SystemError || to == tes.Canceled {
			return nil
		}
		return &TransitionError{from, to}

	case tes.ExecutorError, tes.SystemError:
		// May not transition out of these terminal state, except in the case of a retry.
		// Whether to allow retries could be made into a configuration if needed
		if to == tes.Queued || to == tes.Initializing {
			return nil
		}
		return &TransitionError{from, to}

	case tes.Complete, tes.Canceled:
		// May not transition out of these terminal states
		return &TransitionError{from, to}

	}

	// Shouldn't be reaching this point, but just in case.
	return &TransitionError{from, to}
}
