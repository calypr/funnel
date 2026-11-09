package logger

import (
	"os"

	logconfig "github.com/calypr/funnel/api/logger"
)

// Configure configures the logging level and output path.
func (l *Logger) Configure(conf *logconfig.LoggerConfig) {
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
