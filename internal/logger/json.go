package logger

import (
	logconfig "github.com/calypr/funnel/logger"
	"github.com/sirupsen/logrus"
)

type jsonFormatter struct {
	conf *logconfig.JSONFormatConfig
	fmt  *logrus.JSONFormatter
}

func (f *jsonFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	if f.fmt == nil {
		if f.conf == nil {
			f.conf = &logconfig.JSONFormatConfig{}
		}

		f.fmt = &logrus.JSONFormatter{
			DisableHTMLEscape: true,
			DisableTimestamp:  f.conf.DisableTimestamp,
			TimestampFormat:   f.conf.TimestampFormat,
		}
	}
	return f.fmt.Format(entry)
}
