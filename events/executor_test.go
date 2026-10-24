package events

import "testing"

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
