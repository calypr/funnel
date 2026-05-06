// Package logger contains Funnel's logging code.
package logger

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/logrusorgru/aurora"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/sirupsen/logrus"
)

// Log levels
const (
	DebugLevel = "debug"
	InfoLevel  = "info"
	ErrorLevel = "error"
	WarnLevel  = "warn"
)

// Formatter defines a log output formatter.
type Formatter logrus.Formatter

// NewLogger returns a new Logger instance.
func NewLogger(ns string, conf *LoggerConfig) *Logger {
	log := logrus.New()
	base := log.WithFields(map[string]any{"ns": ns})
	l := &Logger{log, base}
	l.Configure(conf)
	return l
}

// Logger handles structured, configurable application logging.
type Logger struct {
	logrus *logrus.Logger
	base   *logrus.Entry
}

// Sub is a shortcut for l.WithFields("ns", ns), it creates a new logger
// which inherits the parent's configuration but changes the namespace.
func (l *Logger) Sub(ns string) *Logger {
	return l.WithFields("ns", ns)
}

// SetLevel sets the level of the logger.
func (l *Logger) SetLevel(lvl string) {
	switch strings.ToLower(lvl) {
	case "debug":
		l.logrus.Level = logrus.DebugLevel
	case "info":
		l.logrus.Level = logrus.InfoLevel
	case "warn", "warning":
		l.logrus.Level = logrus.WarnLevel
	case "error":
		l.logrus.Level = logrus.ErrorLevel
	default:
		l.logrus.Level = logrus.InfoLevel
	}
}

// SetFormatter sets the formatter of the logger.
func (l *Logger) SetFormatter(f Formatter) {
	l.logrus.Formatter = f
}

// SetOutput sets the output of the logger.
func (l *Logger) SetOutput(o io.Writer) {
	l.logrus.Out = o
}

// Discard configures the logger to discard all logs.
func (l *Logger) Discard() {
	l.SetOutput(io.Discard)
}

// Debug logs a debug message.
//
// After the first argument, arguments are key-value pairs which are written as structured logs.
//
//	log.Debug("Some message here", "key1", value1, "key2", value2)
func (l *Logger) Debug(msg string, args ...any) {
	if l == nil {
		return
	}
	defer recoverLogErr()
	f := util.ArgListToMap(args...)
	l.base.WithFields(f).Debug(msg)
}

// Info logs an info message
//
// After the first argument, arguments are key-value pairs which are written as structured logs.
//
//	log.Info("Some message here", "key1", value1, "key2", value2)
func (l *Logger) Info(msg string, args ...any) {
	if l == nil {
		return
	}
	defer recoverLogErr()
	f := util.ArgListToMap(args...)
	l.base.WithFields(f).Info(msg)
}

// Error logs an error message
//
// After the first argument, arguments are key-value pairs which are written as structured logs.
//
//	log.Error("Some message here", "key1", value1, "key2", value2)
//
// Error has a two-argument version that can be used as a shortcut.
//
//	err := startServer()
//	log.Error("Couldn't start server", err)
func (l *Logger) Error(msg string, args ...any) {
	if l == nil {
		return
	}
	defer recoverLogErr()
	var f map[string]any
	if len(args) == 1 {
		f = util.ArgListToMap("error", args[0])
	} else {
		f = util.ArgListToMap(args...)
	}
	//l.base.WithFields(f).Error(msg)
	if _, ok := f["src"]; !ok {
		if pc, file, line, ok := runtime.Caller(1); ok {
			file = file[strings.LastIndex(file, "/")+1:]
			funcName := runtime.FuncForPC(pc).Name()
			f["src"] = fmt.Sprintf("%s:%s:%d", file, funcName, line)
		}
	}
	l.base.WithFields(f).Error(msg)
}

// Warn logs an warning message
//
// After the first argument, arguments are key-value pairs which are written as structured logs.
//
//	log.Info("Some message here", "key1", value1, "key2", value2)
func (l *Logger) Warn(msg string, args ...any) {
	if l == nil {
		return
	}
	defer recoverLogErr()
	f := util.ArgListToMap(args...)
	l.base.WithFields(f).Warn(msg)
}

// WithFields returns a new Logger instance with the given fields added to all log messages.
func (l *Logger) WithFields(args ...any) *Logger {
	if l == nil {
		return l
	}
	defer recoverLogErr()
	f := util.ArgListToMap(args...)
	base := l.base.WithFields(f)
	return &Logger{l.logrus, base}
}

// PrintSimpleError prints out an error message with a red "ERROR:" prefix.
func PrintSimpleError(err error) {
	e := "Error:"
	if isColorTerminal(os.Stderr) {
		e = aurora.Red(e).String()
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", e, err.Error())
}

// recoverLogErr is used to recover from any panics during logging.
// Panics aren't expected of course, but logging should never crash
// a program, so this failsafe tries to prevent those crashes.
func recoverLogErr() {
	if r := recover(); r != nil {
		fmt.Println("Recovered from logging panic", r)
		// Print the stack trace to stderr
		debug.PrintStack()
	}
}
