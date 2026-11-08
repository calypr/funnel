package events

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type logDeliveryWriter struct {
	mu       sync.Mutex
	attempts map[Type]int
	saved    map[Type]string
	fail     func(int) error
}

func (w *logDeliveryWriter) Close() {}
func (w *logDeliveryWriter) WriteEvent(ctx context.Context, e *Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts[e.Type]++
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return status.Error(codes.InvalidArgument, "missing delivery deadline")
	}
	if err := w.fail(w.attempts[e.Type]); err != nil {
		return err
	}
	w.saved[e.Type] = e.GetStdout() + e.GetStderr()
	return nil
}

func TestLogTailDeliveryAfterTransientFailure(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, failure := range []struct {
			name string
			err  error
		}{
			{"ContextDeadline", context.DeadlineExceeded},
			{"DeadlineExceeded", status.Error(codes.DeadlineExceeded, "deadline")},
			{"Unavailable", status.Error(codes.Unavailable, "unavailable")},
			{"ResourceExhausted", status.Error(codes.ResourceExhausted, "exhausted")},
		} {
			name := "final/" + failure.name
			if streaming {
				name = "stream/" + failure.name
			}
			t.Run(name, func(t *testing.T) {
				out := &logDeliveryWriter{
					attempts: make(map[Type]int), saved: make(map[Type]string),
					fail: func(attempt int) error {
						if attempt == 1 {
							return failure.err
						}
						return nil
					},
				}
				runLogDelivery(t, streaming, out)
				out.mu.Lock()
				defer out.mu.Unlock()
				if got := out.saved[Type_EXECUTOR_STDOUT]; got != "done!\n" {
					t.Errorf("stdout = %q", got)
				}
				if got := out.saved[Type_EXECUTOR_STDERR]; got != "diagnostic\n" {
					t.Errorf("stderr = %q", got)
				}
			})
		}
	}
}

func TestLogTailDeliveryStopsAfterPersistentFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			out := &logDeliveryWriter{
				attempts: make(map[Type]int), saved: make(map[Type]string),
				fail: func(int) error { return status.Error(code, "persistent failure") },
			}
			runLogDelivery(t, true, out)
			out.mu.Lock()
			defer out.mu.Unlock()
			want := 3
			if code == codes.PermissionDenied {
				want = 1
			}
			for _, typ := range []Type{Type_EXECUTOR_STDOUT, Type_EXECUTOR_STDERR} {
				if got := out.attempts[typ]; got != want {
					t.Errorf("%s: attempts = %d, want %d", typ, got, want)
				}
			}
		})
	}
}

func runLogDelivery(t *testing.T, streaming bool, out Writer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var stdout, stderr io.Writer
	if streaming {
		stdout, stderr = StreamLogTail(ctx, "task", 0, 0, 1024, time.Hour, out, &wg)
	} else {
		stdout, stderr = LogTail(ctx, "task", 0, 0, 1024, out, &wg)
	}
	for _, chunk := range []struct {
		out  io.Writer
		text string
	}{{stdout, "done!\n"}, {stderr, "diagnostic\n"}} {
		if _, err := io.WriteString(chunk.out, chunk.text); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("log delivery did not stop after cancellation")
	}
}

func TestStreamLogTailRetryPreservesLatestSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan struct{})
	resume := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(resume) })
	out := &logDeliveryWriter{
		attempts: make(map[Type]int), saved: make(map[Type]string),
		fail: func(attempt int) error {
			if attempt == 1 {
				close(first)
				<-resume
				return status.Error(codes.Unavailable, "temporary delivery failure")
			}
			return nil
		},
	}
	var wg sync.WaitGroup
	stdout, _ := StreamLogTail(ctx, "task", 0, 0, 1024, time.Millisecond, out, &wg)
	if _, err := io.WriteString(stdout, "first\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first snapshot was not delivered")
	}
	if _, err := io.WriteString(stdout, "second\n"); err != nil {
		t.Fatal(err)
	}
	cancel()
	release.Do(func() { close(resume) })
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot delivery did not finish")
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if got := out.saved[Type_EXECUTOR_STDOUT]; got != "first\nsecond\n" {
		t.Fatalf("latest stdout = %q", got)
	}
}
