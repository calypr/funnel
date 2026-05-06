package logger

import (
	"os"
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

// Configure configures the logging level and output path.
func (l *Logger) Configure(conf *LoggerConfig) {
	l.SetLevel(conf.Level)

	switch conf.Formatter {
	case "json":
		l.SetFormatter(&jsonFormatter{
			conf: conf.JsonFormat,
		})

	// Default to text
	default:
		l.SetFormatter(&textFormatter{
			conf.TextFormat,
			jsonFormatter{
				conf: conf.JsonFormat,
			},
		})
	}

	// TODO Good defaults, configuration, and reusable way to configure logging.
	//      Also, how do we get this to default to /var/log/tes/worker.log
	//      without having file permission problems? syslog?
	if conf.OutputFile != "" {
		logFile, err := os.OpenFile(
			conf.OutputFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666,
		)
		if err != nil {
			l.Error("Can't open log output", "output", conf.OutputFile)
		} else {
			l.SetOutput(logFile)
		}
	}
}
