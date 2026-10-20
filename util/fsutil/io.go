package fsutil

import (
	"context"
	"io"
	"time"
)

// Reader wraps an io.Reader with one that checks ctx.Done() on each Read call.
//
// If ctx has a deadline and if r has a `SetReadDeadline(time.Time) error` method,
// then it is called with the deadline.
//
// Imported from https://gist.github.com/dchapes/6c992bf3e943934462509338cd213e99
func Reader(ctx context.Context, r io.Reader) io.Reader {
	if deadline, ok := ctx.Deadline(); ok {
		type deadliner interface {
			SetReadDeadline(time.Time) error
		}
		if d, ok := r.(deadliner); ok {
			_ = d.SetReadDeadline(deadline)
		}
	}
	return reader{ctx, r}
}

type reader struct {
	ctx context.Context
	r   io.Reader
}

func (r reader) Read(p []byte) (n int, err error) {
	if err = r.ctx.Err(); err != nil {
		return
	}
	if n, err = r.r.Read(p); err != nil {
		return
	}
	err = r.ctx.Err()
	return
}

// Writer wraps an io.Writer with one that checks ctx.Done() on each Write call.
//
// If ctx has a deadline and if w has a `SetWriteDeadline(time.Time) error` method,
// then it is called with the deadline.
//
// Imported from https://gist.github.com/dchapes/6c992bf3e943934462509338cd213e99
func Writer(ctx context.Context, w io.Writer) io.Writer {
	if deadline, ok := ctx.Deadline(); ok {
		type deadliner interface {
			SetWriteDeadline(time.Time) error
		}
		if d, ok := w.(deadliner); ok {
			_ = d.SetWriteDeadline(deadline)
		}
	}
	return writer{ctx, w}
}

type writer struct {
	ctx context.Context
	w   io.Writer
}

func (w writer) Write(p []byte) (n int, err error) {
	if err = w.ctx.Err(); err != nil {
		return
	}
	if n, err = w.w.Write(p); err != nil {
		return
	}
	err = w.ctx.Err()
	return
}
