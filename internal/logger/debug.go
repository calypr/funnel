package logger

import (
	"time"

	logconfig "github.com/calypr/funnel/api/logger"
)

var debug_logger = NewLogger("debug", DebugConfig())

// Debug logs debug messages to a global logger.
func Debug(msg string, args ...interface{}) {
	debug_logger.Debug(msg, args...)
}

// DebugConfig returns a Config instance with default values useful for testing/debugging.
func DebugConfig() *logconfig.LoggerConfig {
	return &logconfig.LoggerConfig{
		Level:     "debug",
		Formatter: "text",
		TextFormat: &logconfig.TextFormatConfig{
			ForceColors:     true,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		},
	}
}
