package logger

import "strings"

// ANSI SGR escape helpers, replacing the aurora dependency.

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiBold   = "\x1b[1m"
)

// ansiColorize wraps s in the given SGR escape sequences.
func ansiColorize(s string, codes ...string) string {
	return strings.Join(codes, "") + s + ansiReset
}

func ansiRedS(s string) string        { return ansiColorize(s, ansiRed) }
func ansiBoldRed(s string) string     { return ansiColorize(s, ansiRed, ansiBold) }
func ansiBoldYellow(s string) string  { return ansiColorize(s, ansiYellow, ansiBold) }
func ansiBoldCyan(s string) string    { return ansiColorize(s, ansiCyan, ansiBold) }
func ansiBoldMagenta(s string) string { return ansiColorize(s, "\x1b[35m", ansiBold) }
