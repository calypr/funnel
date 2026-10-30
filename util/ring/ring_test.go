package ring

import (
	"bytes"
	"testing"
)

func TestBuffer_Reset(t *testing.T) {
	// Write a bunch of data
	inputs := [][]byte{
		[]byte("hello world\n"),
		[]byte("this is a test\n"),
		[]byte("my cool input\n"),
	}

	buf := NewBuffer(4)

	for _, b := range inputs {
		n, err := buf.Write(b)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if n != len(b) {
			t.Fatalf("bad: %v", n)
		}
	}

	// Reset it
	buf.Reset()

	if buf.TotalWritten() != 0 {
		t.Fatalf("expected total written to be 0 after reset, got %d", buf.TotalWritten())
	}
	if buf.NewBytesWritten() != 0 {
		t.Fatalf("expected new bytes written to be 0 after reset, got %d", buf.TotalWritten())
	}

	// Write more data
	input := []byte("hello")
	n, err := buf.Write(input)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if n != len(input) {
		t.Fatalf("bad: %v", n)
	}

	// Test the output
	expect := []byte("ello")
	if !bytes.Equal(buf.Bytes(), expect) {
		t.Fatalf("bad: %v", string(buf.Bytes()))
	}
}

func TestBuffer_ResetNewBytes(t *testing.T) {

	buf := NewBuffer(4)
	if _, err := buf.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}

	if buf.NewBytesWritten() != 5 {
		t.Fatalf("expected new bytes written to be 5, got %d", buf.NewBytesWritten())
	}

	if buf.String() != "2345" {
		t.Fatalf("expected content to be 2345, got %s", buf.String())
	}
	buf.ResetNewBytesWritten()

	if buf.NewBytesWritten() != 0 {
		t.Fatalf("expected new bytes written to be 0, got %d", buf.NewBytesWritten())
	}

	if buf.String() != "2345" {
		t.Fatalf("expected content to be 2345, got %s", buf.String())
	}

	if _, err := buf.Write([]byte("6789")); err != nil {
		t.Fatal(err)
	}
	if buf.NewBytesWritten() != 4 {
		t.Fatalf("expected new bytes written to be 4, got %d", buf.NewBytesWritten())
	}

	if buf.String() != "6789" {
		t.Fatalf("expected content to be 6789, got %s", buf.String())
	}
}
