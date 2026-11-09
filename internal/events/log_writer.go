package events

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/calypr/funnel/internal/logger"
)

// Logger writes events to a funnel logger.
type Logger struct {
	Log *logger.Logger
}

// WriteEvent writes an event to the logger.
func (el *Logger) WriteEvent(ctx context.Context, ev *Event) error {
	ts := ev.Type.String()
	log := el.Log.WithFields(
		"taskID", ev.Id,
		"attempt", ev.Attempt,
		"index", ev.Index,
		"timestamp", ev.Timestamp,
	)

	switch ev.Type {
	case Type_TASK_STATE:
		log.Info(ts, "state", ev.GetState().String())
	case Type_TASK_START_TIME:
		log.Info(ts, "start_time", ev.GetStartTime())
	case Type_TASK_END_TIME:
		log.Info(ts, "end_time", ev.GetEndTime())
	case Type_TASK_OUTPUTS:
		log.Info(ts, "outputs", "[redacted]")
	case Type_TASK_METADATA:
		log.Info(ts, "metadata", "[redacted]")
	case Type_EXECUTOR_START_TIME:
		log.Info(ts, "start_time", ev.GetStartTime())
	case Type_EXECUTOR_END_TIME:
		log.Info(ts, "end_time", ev.GetEndTime())
	case Type_EXECUTOR_EXIT_CODE:
		log.Info(ts, "exit_code", ev.GetExitCode())
	case Type_EXECUTOR_STDOUT:
		log.Info(ts, "stdout", "[redacted]")
	case Type_EXECUTOR_STDERR:
		log.Info(ts, "stderr", "[redacted]")
	case Type_SYSTEM_LOG:
		switch ev.GetSystemLog().Level {
		case "warning":
			log.Warn("system log", "severity", "warning")
		case "error":
			if pc, file, line, ok := runtime.Caller(2); ok {
				file = file[strings.LastIndex(file, "/")+1:]
				funcName := runtime.FuncForPC(pc).Name()
				src := fmt.Sprintf("%s:%s:%d", file, funcName, line)
				log.Error("system log", "severity", "error", "src", src)
			} else {
				log.Error("system log", "severity", "error")
			}
		case "info":
			log.Info("system log", "severity", "info")
		case "debug":
			log.Debug("system log", "severity", "debug")
		}
	default:
		log.Info(ts)
	}
	return nil
}

func (el *Logger) Close() {}
