package fsutil

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

type deadlineFailure struct{ failure error }

func (d deadlineFailure) SetReadDeadline(time.Time) error  { return d.failure }
func (d deadlineFailure) SetWriteDeadline(time.Time) error { return d.failure }
func (deadlineFailure) Read([]byte) (int, error)           { return 1, nil }
func (deadlineFailure) Write([]byte) (int, error)          { return 1, nil }
func TestDeadlineSetupFailureIsReturned(t *testing.T) {
	failure := errors.New("deadline setup failed")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	input := deadlineFailure{failure}
	if _, err := Reader(ctx, input).Read(make([]byte, 1)); !errors.Is(err, failure) {
		t.Errorf("Read returned %v, want deadline failure", err)
	}
	if _, err := Writer(ctx, input).Write([]byte("x")); !errors.Is(err, failure) {
		t.Errorf("Write returned %v, want deadline failure", err)
	}
}

func TestUnsupportedFileDeadlineStillAllowsIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	input := deadlineFailure{os.ErrNoDeadline}
	if _, err := Reader(ctx, input).Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Writer(ctx, input).Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
}
