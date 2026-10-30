package events

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestFileWriterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	event := NewSystemLog("task", 0, 0, "info", "multiline\nmessage", map[string]string{"GitCommit": "abc123"})
	for i := 0; i < 2; i++ {
		w, err := openTestNDJSONWriter(t, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.WriteEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		w.Close()
		w.Close()
		if err := w.WriteEvent(context.Background(), event); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed write: %v", err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var decoded Event
		if err := protojson.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(event, &decoded) {
			t.Fatalf("event changed: %v", &decoded)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d records", count)
	}
}

func TestFileWriterConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := openTestNDJSONWriter(t, path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.WriteEvent(context.Background(), NewSystemLog("task", 0, 0, "info", "hello", nil)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	w.Close()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var event Event
		if err := protojson.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("got %d records", count)
	}
}

type failedEventOutput struct {
	err   error
	short bool
}

func (w failedEventOutput) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}
func (failedEventOutput) Close() error { return nil }
func TestFileWriterFailures(t *testing.T) {
	failure := errors.New("disk full")
	for _, tt := range []struct {
		out  failedEventOutput
		want error
	}{{failedEventOutput{err: failure}, failure}, {failedEventOutput{short: true}, io.ErrShortWrite}} {
		writer := NewNDJSONWriter(tt.out)
		err := writer.WriteEvent(context.Background(), NewSystemLog("task", 0, 0, "info", "hello", nil))
		if !errors.Is(err, tt.want) {
			t.Fatalf("got %v want %v", err, tt.want)
		}
	}

}

func TestFileWriterEventTypes(t *testing.T) {
	for _, event := range []*Event{
		NewTaskCreated(&tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"echo", "hello"}}}}),
		NewState("task", tes.State_RUNNING),
		NewOutputs("task", 0, []*tes.OutputFileLog{{Path: "/result", SizeBytes: "123"}}),
	} {
		path := filepath.Join(t.TempDir(), "events.ndjson")
		writer, err := openTestNDJSONWriter(t, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		writer.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Event
		if err := Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(event, &decoded) {
			t.Fatalf("round trip changed event: %v", &decoded)
		}
	}
}

func openTestNDJSONWriter(t *testing.T, path string) (*NDJSONWriter, error) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = file.Close() })
	return NewNDJSONWriter(file), nil
}

func TestNDJSONWriterDoesNotOwnOutput(t *testing.T) {
	var output trackedOutput
	writer := NewNDJSONWriter(&output)
	writer.Close()
	if output.closed {
		t.Fatal("encoder closed caller-owned output")
	}
	if _, err := output.WriteString("still available"); err != nil {
		t.Fatal(err)
	}
	if output.String() != "still available" {
		t.Fatal("output unavailable")
	}
}

type trackedOutput struct {
	bytes.Buffer
	closed bool
}

func (o *trackedOutput) Close() error { o.closed = true; return nil }
