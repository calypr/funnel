package events

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ohsu-comp-bio/funnel/util/ring"
	"golang.org/x/time/rate"
)

// Global counters for tracking log event drops (for debugging/metrics)
var (
	stdoutEventsDropped int64
	stderrEventsDropped int64
	logEventsTotal      int64
)

// GetLogEventStats returns current log event statistics
func GetLogEventStats() (stdoutDropped, stderrDropped, total int64) {
	return atomic.LoadInt64(&stdoutEventsDropped),
		atomic.LoadInt64(&stderrEventsDropped),
		atomic.LoadInt64(&logEventsTotal)
}

// ExecutorGenerator is a type that generates Events for an Executor
// of a Task
type ExecutorGenerator struct {
	taskID  string
	attempt uint32
	index   uint32
	sys     *SystemLogGenerator
}

// NewExecutorGenerator returns a ExecutorGenerator instance.
func NewExecutorGenerator(taskID string, attempt uint32, index uint32) *ExecutorGenerator {
	return &ExecutorGenerator{taskID, attempt, index, &SystemLogGenerator{taskID, attempt, index}}
}

// StartTime updates an executor's start time log.
func (eg *ExecutorGenerator) StartTime(t time.Time) *Event {
	return NewExecutorStartTime(eg.taskID, eg.attempt, eg.index, t)
}

// EndTime updates an executor's end time log.
func (eg *ExecutorGenerator) EndTime(t time.Time) *Event {
	return NewExecutorEndTime(eg.taskID, eg.attempt, eg.index, t)
}

// ExitCode updates an executor's exit code log.
func (eg *ExecutorGenerator) ExitCode(x int) *Event {
	return NewExitCode(eg.taskID, eg.attempt, eg.index, int32(x))
}

// Stdout appends to an executor's stdout log.
func (eg *ExecutorGenerator) Stdout(s string) *Event {
	return NewStdout(eg.taskID, eg.attempt, eg.index, s)
}

// Stderr appends to an executor's stderr log.
func (eg *ExecutorGenerator) Stderr(s string) *Event {
	return NewStderr(eg.taskID, eg.attempt, eg.index, s)
}

// Info creates an info level system log message.
func (eg *ExecutorGenerator) Info(msg string, args ...interface{}) *Event {
	return eg.sys.Info(msg, args...)
}

// Debug creates a debug level system log message.
func (eg *ExecutorGenerator) Debug(msg string, args ...interface{}) *Event {
	return eg.sys.Debug(msg, args...)
}

// Error creates an error level system log message.
func (eg *ExecutorGenerator) Error(msg string, args ...interface{}) *Event {
	return eg.sys.Error(msg, args...)
}

// ExecutorWriter is a type that generates and writes executor events.
type ExecutorWriter struct {
	gen *ExecutorGenerator
	sys *SystemLogWriter
	out Writer
}

// NewExecutorWriter returns a ExecutorWriter instance.
func NewExecutorWriter(taskID string, attempt uint32, index uint32, w Writer) *ExecutorWriter {
	g := NewExecutorGenerator(taskID, attempt, index)
	return &ExecutorWriter{
		gen: g,
		out: w,
		sys: &SystemLogWriter{g.sys, w},
	}
}

// StartTime updates the task's start time log.
func (ew *ExecutorWriter) StartTime(t time.Time) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.StartTime(t))
}

// EndTime updates the task's end time log.
func (ew *ExecutorWriter) EndTime(t time.Time) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.EndTime(t))
}

// ExitCode updates an executor's exit code log.
func (ew *ExecutorWriter) ExitCode(x int) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.ExitCode(x))
}

// Stdout appends to an executor's stdout log.
func (ew *ExecutorWriter) Stdout(s string) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.Stdout(s))
}

// Stderr appends to an executor's stderr log.
func (ew *ExecutorWriter) Stderr(s string) error {
	return ew.out.WriteEvent(context.Background(), ew.gen.Stderr(s))
}

// Info writes an info level system log message.
func (ew *ExecutorWriter) Info(msg string, args ...interface{}) error {
	return ew.sys.Info(msg, args...)
}

// Debug writes a debug level system log message.
func (ew *ExecutorWriter) Debug(msg string, args ...interface{}) error {
	return ew.sys.Debug(msg, args...)
}

// Error writes an error level system log message.
func (ew *ExecutorWriter) Error(msg string, args ...interface{}) error {
	return ew.sys.Error(msg, args...)
}

// LogTail returns stdout/err io.Writers which will track the
// tail of the content (up to "size") and emit events.
func (ew *ExecutorWriter) LogTail(ctx context.Context, size int64) (stdout, stderr io.Writer) {
	return LogTail(ctx, ew.gen.taskID, ew.gen.attempt, ew.gen.index, size, ew.out)
}

// StreamLogTail returns stdout/err io.Writers which will track the
// tail of the content (up to "size") and emit events. Events
// are rate limited by "interval", e.g. a max of one event every
// 5 seconds.
func (ew *ExecutorWriter) StreamLogTail(ctx context.Context, size int64, interval time.Duration, wg *sync.WaitGroup) (stdout, stderr io.Writer) {
	return StreamLogTail(ctx, ew.gen.taskID, ew.gen.attempt, ew.gen.index, size, interval, ew.out, wg)
}

// LogTail returns stdout/err io.Writers which will track the
// tail of the content (up to "size") and emit events once the ctx.Done
// is closed.
func LogTail(ctx context.Context, taskID string, attempt, index uint32, size int64, out Writer) (stdout, stderr io.Writer) {
	stdoutbuf := ring.NewBuffer(size)
	stderrbuf := ring.NewBuffer(size)

	flush := func(buf *ring.Buffer, t Type) error {
		// Only flush if new bytes have been written to the buffer.
		if buf.TotalWritten() == 0 {
			return nil
		}

		// Create the event
		var e *Event
		s := buf.String()
		switch t {
		case Type_EXECUTOR_STDOUT:
			e = NewStdout(taskID, attempt, index, s)
		case Type_EXECUTOR_STDERR:
			e = NewStderr(taskID, attempt, index, s)
		}
		return out.WriteEvent(ctx, e)
	}

	go func() {
		<-ctx.Done()
		flush(stdoutbuf, Type_EXECUTOR_STDOUT)
		flush(stdoutbuf, Type_EXECUTOR_STDERR)
	}()

	return stdoutbuf, stderrbuf
}

// StreamLogTail returns stdout/err io.Writers which will track the
// tail of the content (up to "size") and emit events. Events
// are rate limited by "interval", e.g. a max of one event every
// 5 seconds.
func StreamLogTail(ctx context.Context, taskID string, attempt, index uint32, size int64, interval time.Duration, out Writer, wg *sync.WaitGroup) (stdout, stderr io.Writer) {

	// The rate limiter allows the input writers to trigger events
	// immediately, without waiting for the ticker, as long as
	// they are not exceeding the rate limit.
	limiter := rate.NewLimiter(rate.Every(interval), 1)

	stdoutbuf := ring.NewBuffer(size)
	stderrbuf := ring.NewBuffer(size)
	stdoutch := make(chan []byte)
	stderrch := make(chan []byte)
	// Buffer event channel to handle bursts and prevent silent drops
	eventch := make(chan *Event, 100)
	// Used as an immediate timeout for flush()
	immediate := make(chan time.Time)
	close(immediate)

	flush := func(buf *ring.Buffer, t Type, timeout <-chan time.Time) {
		// Only flush if new bytes have been written to the buffer.
		if buf.NewBytesWritten() == 0 {
			return
		}

		// Create the event
		var e *Event
		s := buf.String()
		switch t {
		case Type_EXECUTOR_STDOUT:
			e = NewStdout(taskID, attempt, index, s)
		case Type_EXECUTOR_STDERR:
			e = NewStderr(taskID, attempt, index, s)
		}

		// Send the event to the routine which is writing out events.
		// With buffered channel, we should rarely encounter busy writer now.
		select {
		case eventch <- e:
			// The writer routine accepted the event, so reset the buffer byte count.
			buf.ResetNewBytesWritten()
		case <-timeout:
			// The writer was busy - increment counter to track this
			switch t {
			case Type_EXECUTOR_STDOUT:
				atomic.AddInt64(&stdoutEventsDropped, 1)
			case Type_EXECUTOR_STDERR:
				atomic.AddInt64(&stderrEventsDropped, 1)
			}
			atomic.AddInt64(&logEventsTotal, 1)
		}
	}

	flushboth := func(timeout <-chan time.Time) {
		flush(stdoutbuf, Type_EXECUTOR_STDOUT, timeout)
		flush(stderrbuf, Type_EXECUTOR_STDERR, timeout)
	}

	// There are two routines below, one for accepting input, one for writing
	// out events. They are separated so that writing out events does not block
	// the input writes. If input writes are faster than output event writes,
	// flush() calls will be dropped. This is ok, because we're flushing the
	// whole buffer (log tail) every tick, so when the output event writer
	// catches up, it will write the new, complete tail.

	wg.Add(1)
	// output event writer routine
	go func() {
		defer wg.Done()

		for e := range eventch {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			err := out.WriteEvent(ctx, e)
			cancel()

			// Track successful events
			if err == nil {
				switch e.Type {
				case Type_EXECUTOR_STDOUT:
					atomic.AddInt64(&logEventsTotal, 1)
				case Type_EXECUTOR_STDERR:
					atomic.AddInt64(&logEventsTotal, 1)
				}
			}
		}
	}()

	// input writes and flush routine.
	go func() {
		// The ticker helps ensure content gets flushed at a regular
		// interval, so nothing is buffered for too long.
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				timeout := time.After(time.Second * 5)
				flushboth(timeout)
				close(eventch)
				return
			case <-ticker.C:
				w := stdoutbuf.NewBytesWritten() + stderrbuf.NewBytesWritten()
				// Don't use a limiter token if no content has been written.
				if w > 0 && limiter.Allow() {
					flushboth(immediate)
				}
			case b := <-stdoutch:
				stdoutbuf.Write(b)
				if limiter.Allow() {
					flushboth(immediate)
				}
			case b := <-stderrch:
				stderrbuf.Write(b)
				if limiter.Allow() {
					flushboth(immediate)
				}
			}
		}
	}()

	return &logTailWriter{stdoutch}, &logTailWriter{stderrch}
}

type logTailWriter struct {
	ch chan<- []byte
}

func (l *logTailWriter) Write(p []byte) (n int, err error) {
	l.ch <- p
	return len(p), nil
}
