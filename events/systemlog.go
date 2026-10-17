package events

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/funnel/util"
)

// SystemLogGenerator is a type that emulates the logger interface
// and outputs Events.
type SystemLogGenerator struct {
	taskID  string
	attempt uint32
	index   uint32
}

// Info creates an Event for an 'info' level log message
func (sle *SystemLogGenerator) Info(msg string, args ...interface{}) *Event {
	return NewSystemLog(sle.taskID, sle.attempt, sle.index, "info", msg, fields(args...))
}

// Debug creates an Event for a 'debug' level log message
func (sle *SystemLogGenerator) Debug(msg string, args ...interface{}) *Event {
	return NewSystemLog(sle.taskID, sle.attempt, sle.index, "debug", msg, fields(args...))
}

// Error creates an Event for an 'error' level log message
func (sle *SystemLogGenerator) Error(msg string, args ...interface{}) *Event {
	return NewSystemLog(sle.taskID, sle.attempt, sle.index, "error", msg, fields(args...))
}

// Warn creates an Event for an 'warning' level log message
func (sle *SystemLogGenerator) Warn(msg string, args ...interface{}) *Event {
	return NewSystemLog(sle.taskID, sle.attempt, sle.index, "warning", msg, fields(args...))
}

// SystemLogWriter is a type that emulates the logger interface
// and outputs Events.
type SystemLogWriter struct {
	gen *SystemLogGenerator
	out Writer
}

// Info writes an Event for an 'info' level log message
func (sle *SystemLogWriter) Info(msg string, args ...interface{}) error {
	return sle.out.WriteEvent(context.Background(), sle.gen.Info(msg, args...))
}

// Debug writes an  for a 'debug' level log message
func (sle *SystemLogWriter) Debug(msg string, args ...interface{}) error {
	return sle.out.WriteEvent(context.Background(), sle.gen.Debug(msg, args...))
}

// Error writes an Event for an 'error' level log message
func (sle *SystemLogWriter) Error(msg string, args ...interface{}) error {
	return sle.out.WriteEvent(context.Background(), sle.gen.Error(msg, args...))
}

// Warn writes an Event for an 'warning' level log message
func (sle *SystemLogWriter) Warn(msg string, args ...interface{}) error {
	return sle.out.WriteEvent(context.Background(), sle.gen.Warn(msg, args...))
}

// converts an argument list to a map, e.g.
// ("key", value, "key2", value2) => {"key": value, "key2", value2}
func fields(args ...interface{}) map[string]string {
	ss := make(map[string]string)
	si := util.ArgListToMap(args...)
	for k, v := range si {
		ss[k] = fmt.Sprintf("%+v", v)
	}
	return ss
}

// SysLogString returns a flattened string representation of the SystemLog event
func (s *Event) SysLogString() string {
	if s.Type != Type_SYSTEM_LOG {
		return ""
	}
	parts := []string{
		fmt.Sprintf("level='%s'", s.GetSystemLog().Level),
		fmt.Sprintf("msg='%s'", escape(s.GetSystemLog().Msg)),
		fmt.Sprintf("timestamp='%s'", s.Timestamp),
		fmt.Sprintf("task_attempt='%v'", s.Attempt),
		fmt.Sprintf("executor_index='%v'", s.Index),
	}
	for k, v := range s.GetSystemLog().Fields {
		parts = append(parts, fmt.Sprintf("%s='%s'", safeKey(k), escape(v)))
	}
	return strings.Join(parts, " ")
}

func escape(s string) string {
	return strings.Replace(s, "'", "\\'", -1)
}

func safeKey(s string) string {
	re := regexp.MustCompile(`[\s]+`)
	return re.ReplaceAllString(s, "_")
}
