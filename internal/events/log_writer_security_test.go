package events

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/proto"
)

func TestLoggerCensorsSensitiveEventPayloads(t *testing.T) {
	const secret = "EVENT_PAYLOAD_SENTINEL"
	log, output := eventCaptureLogger(t)
	writer := &Logger{Log: log}
	events := []*Event{
		NewTaskCreated(&tes.Task{
			Id: "task-log-test",
			Executors: []*tes.Executor{{
				Image:   "alpine",
				Command: []string{"echo", secret},
				Env:     map[string]string{"TOKEN": secret},
			}},
		}),
		NewOutputs("task-log-test", 2, []*tes.OutputFileLog{{Path: secret}}),
		NewMetadata("task-log-test", 2, map[string]string{"private": secret}),
		NewStdout("task-log-test", 2, 3, secret),
		NewStderr("task-log-test", 2, 3, secret),
		NewSystemLog("task-log-test", 2, 3, "warning", secret, map[string]string{"payload": secret}),
	}

	for _, ev := range events {
		before := proto.Clone(ev).(*Event)
		output.Reset()
		if err := writer.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent(%s) error = %v", ev.Type, err)
		}
		got := output.String()
		if strings.Contains(got, secret) {
			t.Errorf("%s log contains event payload sentinel: %s", ev.Type, got)
		}
		if ev.Type == Type_TASK_CREATED && !strings.Contains(got, ev.Type.String()) {
			t.Errorf("task-created log omits event type: %s", got)
		}
		if !proto.Equal(ev, before) {
			t.Errorf("%s event changed while logging", ev.Type)
		}
		for _, field := range []string{"taskID", "attempt", "index", "timestamp"} {
			if !strings.Contains(got, field) {
				t.Errorf("%s log omits %q identity metadata: %s", ev.Type, field, got)
			}
		}
	}
}

func TestErrLoggerCensorsPayloadAndPreservesWriteError(t *testing.T) {
	const secret = "RAW_EVENT_ERROR_SENTINEL"
	writeErr := errors.New(secret)
	log, output := eventCaptureLogger(t)
	ev := NewStdout("task-error-test", 4, 5, secret)
	before := proto.Clone(ev).(*Event)
	writer := &ErrLogger{Writer: failingEventWriter{err: writeErr}, Log: log}

	gotErr := writer.WriteEvent(context.Background(), ev)
	if gotErr != writeErr {
		t.Fatalf("WriteEvent error = %v, want original error %v", gotErr, writeErr)
	}
	got := output.String()
	if strings.Contains(got, secret) {
		t.Fatalf("error log contains raw error or event payload: %s", got)
	}
	if !strings.Contains(got, `"error_type":"*errors.errorString"`) {
		t.Errorf("error log omits error classification: %s", got)
	}
	if !strings.Contains(got, `"event_type":"EXECUTOR_STDOUT"`) || !strings.Contains(got, `"taskID":"task-error-test"`) {
		t.Errorf("error log omits event identity: %s", got)
	}
	if !proto.Equal(ev, before) {
		t.Fatal("event changed while failed write was logged")
	}
}

func eventCaptureLogger(t *testing.T) (*logger.Logger, *bytes.Buffer) {
	t.Helper()
	conf := logger.DebugConfig()
	conf.Formatter = "json"
	output := &bytes.Buffer{}
	log := logger.NewLogger("test", conf)
	log.SetOutput(output)
	return log, output
}

type failingEventWriter struct {
	err error
}

func (w failingEventWriter) WriteEvent(context.Context, *Event) error { return w.err }

func (failingEventWriter) Close() {}
