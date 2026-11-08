package logger

import (
	"time"

	logconfig "github.com/calypr/funnel/logger"
)

const defaultTimestampFormat = time.RFC3339

// DefaultConfig returns a Config instance with default values.
func DefaultConfig() *logconfig.LoggerConfig {
	return &logconfig.LoggerConfig{
		Level:     "info",
		Formatter: "text",
		TextFormat: &logconfig.TextFormatConfig{
			FullTimestamp:   true,
			TimestampFormat: defaultTimestampFormat,
		},
	}
}
