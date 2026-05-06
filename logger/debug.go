package logger

var debug_logger = NewLogger("debug", DebugConfig())

// Debug logs debug messages to a global logger.
func Debug(msg string, args ...interface{}) {
	debug_logger.Debug(msg, args...)
}

// DebugConfig returns a Config instance with default values useful for testing/debugging.
func DebugConfig() *LoggerConfig {
	return &LoggerConfig{
		Level:     "debug",
		Formatter: "text",
		TextFormat: &TextFormatConfig{
			ForceColors:     true,
			FullTimestamp:   true,
			TimestampFormat: defaultTimestampFormat,
		},
	}
}
