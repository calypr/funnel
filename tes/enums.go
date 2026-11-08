package tes

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

// Shorthand for TES views and file types.
const (
	Minimal   = View_MINIMAL
	Basic     = View_BASIC
	Full      = View_FULL
	File      = FileType_FILE
	Directory = FileType_DIRECTORY
)

// TerminalState reports whether a task has finished.
func TerminalState(s State) bool {
	return s == State_COMPLETE || s == State_EXECUTOR_ERROR || s == State_SYSTEM_ERROR || s == State_CANCELED
}
