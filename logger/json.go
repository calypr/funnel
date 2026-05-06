package logger

import (
	"github.com/sirupsen/logrus"
)

type jsonFormatter struct {
	conf *JSONFormatConfig
	fmt  *logrus.JSONFormatter
}

func (f *jsonFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	if f.fmt == nil {
		if f.conf == nil {
			f.conf = &JSONFormatConfig{}
		}

		f.fmt = &logrus.JSONFormatter{
			DisableHTMLEscape: true,
			DisableTimestamp:  f.conf.DisableTimestamp,
			TimestampFormat:   f.conf.TimestampFormat,
		}
	}
	return f.fmt.Format(entry)
}
