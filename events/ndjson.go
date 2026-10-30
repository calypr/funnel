package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// NDJSONWriter serializes canonical events as NDJSON. Close stops writes but does not close the output.
type NDJSONWriter struct {
	mu      sync.Mutex
	output  io.Writer
	closed  bool
	failure error
}

// NewNDJSONWriter writes canonical events without taking ownership of output.
func NewNDJSONWriter(output io.Writer) *NDJSONWriter {
	return &NDJSONWriter{output: output}
}

func (w *NDJSONWriter) WriteEvent(ctx context.Context, event *Event) error {
	if event == nil {
		return fmt.Errorf("cannot write a nil event")
	}
	data, err := Marshal(event)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(data)); err != nil {
		return err
	}
	compact.WriteByte('\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.closed {
		return os.ErrClosed
	}
	if w.failure != nil {
		return w.failure
	}
	n, err := w.output.Write(compact.Bytes())
	if err == nil && n != compact.Len() {
		err = io.ErrShortWrite
	}
	w.failure = err
	return err
}

func (w *NDJSONWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
	}
}
