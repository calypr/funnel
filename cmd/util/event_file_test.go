package util

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
)

type failingEventCloser struct{ calls int }

func (f *failingEventCloser) Close() error { f.calls++; return errors.New("close failed") }
func TestEventFileCloseReportsErrorOnce(t *testing.T) {
	var logs, output bytes.Buffer
	log := logger.NewLogger("test", logger.DefaultConfig())
	log.SetOutput(&logs)
	file := &failingEventCloser{}
	writer := &eventFileWriter{NDJSONWriter: events.NewNDJSONWriter(&output), file: file, log: log}
	writer.Close()
	writer.Close()
	if file.calls != 1 {
		t.Fatalf("closed %d times", file.calls)
	}
	if !strings.Contains(logs.String(), "close failed") {
		t.Fatalf("close error missing: %s", logs.String())
	}
}
func TestEventFileRequiresPath(t *testing.T) {
	if _, err := NewEventFileWriter("", logger.NewLogger("test", logger.DefaultConfig())); err == nil {
		t.Fatal("empty path accepted")
	}
}
