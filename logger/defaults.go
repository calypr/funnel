package logger

import (
	"time"
)

const defaultTimestampFormat = time.RFC3339

// DefaultConfig returns a Config instance with default values.
func DefaultConfig() *LoggerConfig {
	return &LoggerConfig{
		Level:     "info",
		Formatter: "text",
		TextFormat: &TextFormatConfig{
			FullTimestamp:   true,
			TimestampFormat: defaultTimestampFormat,
		},
	}
}
