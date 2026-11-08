package tes

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
