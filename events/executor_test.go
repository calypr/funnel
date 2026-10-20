package events

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type eventCollector struct {
	events chan *Event
}

func (w *eventCollector) WriteEvent(_ context.Context, event *Event) error {
	w.events <- event
	return nil
}

func (w *eventCollector) Close() {}

func TestLogTailWriterOwnsBytesAfterWrite(t *testing.T) {
	chunks := make(chan []byte)
	writer := &logTailWriter{ch: chunks}
	written := make(chan struct{})
	go func() {
		defer close(written)
		data := []byte("original log")
		if n, err := writer.Write(data); err != nil || n != len(data) {
			t.Errorf("Write = %d, %v", n, err)
		}
		copy(data, "reused bytes")
	}()
	chunk := <-chunks
	<-written
	if got := string(chunk); got != "original log" {
		t.Fatalf("log chunk = %q, want original log", got)
	}
}

func TestStreamLogTailWritersFinishAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	out := &eventCollector{events: make(chan *Event, 2)}
	stdout, stderr := StreamLogTail(ctx, "task", 0, 0, 1024, time.Hour, out, &wg)

	for _, tc := range []struct {
		writer io.Writer
		data   string
	}{
		{stdout, "stdout before cancellation"},
		{stderr, "stderr before cancellation"},
	} {
		if n, err := tc.writer.Write([]byte(tc.data)); err != nil || n != len(tc.data) {
			t.Fatalf("Write = %d, %v, want %d, nil", n, err, len(tc.data))
		}
	}

	cancel()
	wg.Wait()

	got := make(map[Type]string, 2)
	for range 2 {
		select {
		case event := <-out.events:
			got[event.Type] = event.GetStdout() + event.GetStderr()
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for final log events")
		}
	}
	if got[Type_EXECUTOR_STDOUT] != "stdout before cancellation" {
		t.Errorf("stdout event = %q", got[Type_EXECUTOR_STDOUT])
	}
	if got[Type_EXECUTOR_STDERR] != "stderr before cancellation" {
		t.Errorf("stderr event = %q", got[Type_EXECUTOR_STDERR])
	}

	results := make(chan error, 2)
	for _, writer := range []io.Writer{stdout, stderr} {
		go func(writer io.Writer) {
			data := []byte("late")
			n, err := writer.Write(data)
			if err == nil && n != len(data) {
				err = io.ErrShortWrite
			}
			results <- err
		}(writer)
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("Write after cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Write remained blocked after cancellation")
		}
	}
}
