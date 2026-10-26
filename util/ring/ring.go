package ring

import "errors"

// Buffer implements a circular buffer. It is a fixed size,
// and new writes overwrite older data, such that for a buffer
// of size N, for any amount of writes, only the last N bytes
// are retained.
type Buffer struct {
	buf      []byte
	size     int64
	head     int64
	total    int64
	newBytes int64
}

// NewBuffer creates a new buffer of a given size.
// NewBuffer panics if the size is not greater than 0.
func NewBuffer(size int64) *Buffer {
	if size <= 0 {
		panic(errors.New("size must be greater than 0"))
	}
	return &Buffer{buf: make([]byte, size), size: size}
}

// Write writes up to len(buf) bytes to the internal ring,
// overriding older data if necessary.
func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += int64(n)
	b.newBytes += int64(n)
	if int64(n) >= b.size {
		copy(b.buf, p[int64(n)-b.size:])
		b.head = 0
	} else {
		tail := b.size - b.head
		if tail > int64(n) {
			tail = int64(n)
		}
		copy(b.buf[b.head:], p[:tail])
		remaining := int64(n) - tail
		if remaining > 0 {
			copy(b.buf, p[tail:])
			b.head = remaining
		} else {
			b.head += tail
			if b.head >= b.size {
				b.head = 0
			}
		}
	}
	return n, nil
}

// Bytes returns a slice of the current buffer contents.
func (b *Buffer) Bytes() []byte {
	if b.total < b.size {
		return b.buf[:b.total]
	}
	out := make([]byte, b.size)
	copy(out, b.buf[b.head:])
	copy(out[b.size-b.head:], b.buf[:b.head])
	return out
}

// String returns the contents of the unread portion of the buffer as a string.
func (b *Buffer) String() string {
	return string(b.Bytes())
}

// Size returns the size of the buffer.
func (b *Buffer) Size() int64 {
	return b.size
}

// TotalWritten returns the total number of bytes ever written to the buffer.
func (b *Buffer) TotalWritten() int64 {
	return b.total
}

// NewBytesWritten provides the total number of bytes written since the last reset.
func (b *Buffer) NewBytesWritten() int64 {
	return b.newBytes
}

// Reset resets the buffer so it has no content.
func (b *Buffer) Reset() {
	b.head = 0
	b.total = 0
	b.newBytes = 0
}

// ResetNewBytesWritten resets the new bytes written counter.
func (b *Buffer) ResetNewBytesWritten() {
	b.newBytes = 0
}
