package util

import (
	"bytes"
	"io"
	"os"
)

// EmptyReader returns an io.Reader which is empty and immediately closed.
func EmptyReader() io.Reader {
	return io.NopCloser(bytes.NewReader(nil))
}

// StdinPipe will return stdin if it's available, otherwise it will return
// EmptyReader()
func StdinPipe() io.Reader {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return EmptyReader()
	}
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		return os.Stdin
	}
	return EmptyReader()
}
