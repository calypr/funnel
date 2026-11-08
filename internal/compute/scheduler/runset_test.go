package scheduler

import (
	"testing"
	"time"
)

func TestRunset(t *testing.T) {
	r := newRunSet()
	r.Add("foo")
	nok := r.Add("foo")
	if nok {
		t.Fatal("Expected duplicate Add to return false")
	}

	go func() {
		time.Sleep(time.Millisecond * 10)
		r.Remove("foo")
	}()

	if r.Count() != 1 {
		t.Fatal("Unexpected runner count")
	}

	err := r.Wait(time.Second)

	if err != nil {
		t.Fatal("unexpected timeout")
	}

	if r.Count() != 0 {
		t.Fatal("Unexpected runner count")
	}

	// Should do nothing
	r.Remove("foo")
	if r.Count() != 0 {
		t.Fatal("Unexpected runner count")
	}
}
