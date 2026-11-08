package events

import (
	"context"
	"time"

	"github.com/calypr/funnel/tes"
)

// TaskGenerator is a type that generates Events for a given Task execution
// attempt.
type TaskGenerator struct {
	taskID  string
	attempt uint32
	sys     *SystemLogGenerator
}

// NewTaskGenerator creates a TaskGenerator instance.
func NewTaskGenerator(taskID string, attempt uint32) *TaskGenerator {
	return &TaskGenerator{taskID, attempt, &SystemLogGenerator{taskID, attempt, 0}}
}

// State sets the state of the task.
func (eg *TaskGenerator) State(s tes.State) *Event {
	return NewState(eg.taskID, s)
}

// StartTime updates the task's start time log.
func (eg *TaskGenerator) StartTime(t time.Time) *Event {
	return NewStartTime(eg.taskID, eg.attempt, t)
}

// EndTime updates the task's end time log.
func (eg *TaskGenerator) EndTime(t time.Time) *Event {
	return NewEndTime(eg.taskID, eg.attempt, t)
}

// Outputs updates the task's output file log.
func (eg *TaskGenerator) Outputs(f []*tes.OutputFileLog) *Event {
	return NewOutputs(eg.taskID, eg.attempt, f)
}

// Metadata updates the task's metadata log.
func (eg *TaskGenerator) Metadata(m map[string]string) *Event {
	return NewMetadata(eg.taskID, eg.attempt, m)
}

// Info creates an info level system log message.
func (eg *TaskGenerator) Info(msg string, args ...interface{}) *Event {
	return eg.sys.Info(msg, args...)
}

// Debug creates a debug level system log message.
func (eg *TaskGenerator) Debug(msg string, args ...interface{}) *Event {
	return eg.sys.Debug(msg, args...)
}

// Error creates an error level system log message.
func (eg *TaskGenerator) Error(msg string, args ...interface{}) *Event {
	return eg.sys.Error(msg, args...)
}

// Warn creates a warning level system log message.
func (eg *TaskGenerator) Warn(msg string, args ...interface{}) *Event {
	return eg.sys.Warn(msg, args...)
}

// TaskWriter is a type that generates and writes task events.
type TaskWriter struct {
	gen *TaskGenerator
	sys *SystemLogWriter
	out Writer
}

// NewTaskWriter returns a TaskWriter instance.
func NewTaskWriter(taskID string, attempt uint32, w Writer) *TaskWriter {
	g := NewTaskGenerator(taskID, attempt)
	return &TaskWriter{
		gen: g,
		out: w,
		sys: &SystemLogWriter{g.sys, w},
	}
}

// State sets the state of the task.
func (ew *TaskWriter) State(s tes.State) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.State(s))
}

// StartTime updates the task's start time log.
func (ew *TaskWriter) StartTime(t time.Time) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.StartTime(t))
}

// EndTime updates the task's end time log.
func (ew *TaskWriter) EndTime(t time.Time) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.EndTime(t))
}

// Outputs updates the task's output file log.
func (ew *TaskWriter) Outputs(f []*tes.OutputFileLog) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.Outputs(f))
}

// Metadata updates the task's metadata log.
func (ew *TaskWriter) Metadata(m map[string]string) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.Metadata(m))
}

// Info creates an info level system log message.
func (ew *TaskWriter) Info(msg string, args ...interface{}) error {
	return ew.sys.Info(msg, args...)
}

// Debug creates a debug level system log message.
func (ew *TaskWriter) Debug(msg string, args ...interface{}) error {
	return ew.sys.Debug(msg, args...)
}

// Error creates an error level system log message.
func (ew *TaskWriter) Error(msg string, args ...interface{}) error {
	return ew.sys.Error(msg, args...)
}

// Warn creates a warning level system log message.
func (ew *TaskWriter) Warn(msg string, args ...interface{}) error {
	return ew.sys.Warn(msg, args...)
}

// NewExecutorWriter returns a new ExecutorEventWriter instance that inherits
// its config from the Task
func (ew *TaskWriter) NewExecutorWriter(index uint32) *ExecutorWriter {
	g := NewExecutorGenerator(ew.gen.taskID, ew.gen.attempt, index)
	return &ExecutorWriter{
		gen: g,
		out: ew.out,
		sys: &SystemLogWriter{g.sys, ew.out},
	}
}
