package tes

import "fmt"

// State variables for convenience
const (
	Unknown       = State_UNKNOWN
	Queued        = State_QUEUED
	Initializing  = State_INITIALIZING
	Running       = State_RUNNING
	Paused        = State_PAUSED
	Complete      = State_COMPLETE
	ExecutorError = State_EXECUTOR_ERROR
	SystemError   = State_SYSTEM_ERROR
	Canceled      = State_CANCELED
)

// TransitionError describes an invalid state transition.
type TransitionError struct {
	From, To State
}

func (te *TransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s",
		te.From.String(), te.To.String())
}

// ValidateTransition validates a task state transition.
// Returns a TransitionError if the transition is not valid.
func ValidateTransition(from, to State) error {

	if from == to {
		return nil
	}

	if from == Paused || to == Paused {
		return fmt.Errorf("paused state is not implemented")
	}

	switch from {
	case Unknown:
		// May transition from Unknown to anything
		return nil

	case Queued:
		// May transition from Queued to anything except Unknown
		if to == Unknown {
			return &TransitionError{from, to}
		}
		return nil

	case Initializing:
		if to == Unknown || to == Queued {
			return &TransitionError{from, to}
		}
		return nil

	case Running:
		if to == Complete || to == ExecutorError || to == SystemError || to == Canceled {
			return nil
		}
		return &TransitionError{from, to}

	case ExecutorError, SystemError:
		// May not transition out of these terminal state, except in the case of a retry.
		// Whether to allow retries could be made into a configuration if needed
		if to == Queued || to == Initializing {
			return nil
		}
		return &TransitionError{from, to}

	case Complete, Canceled:
		// May not transition out of these terminal states
		return &TransitionError{from, to}

	}

	// Shouldn't be reaching this point, but just in case.
	return &TransitionError{from, to}
}
