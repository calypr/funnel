package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// contextReader wraps an io.Reader with one that checks ctx.Done() on each Read call.
//
// If ctx has a deadline and if r has a `SetReadDeadline(time.Time) error` method,
// then it is called with the deadline.
//
// Imported from https://gist.github.com/dchapes/6c992bf3e943934462509338cd213e99
func contextReader(ctx context.Context, r io.Reader) io.Reader {
	if deadline, ok := ctx.Deadline(); ok {
		type deadliner interface {
			SetReadDeadline(time.Time) error
		}
		if d, ok := r.(deadliner); ok {
			err := d.SetReadDeadline(deadline)
			if err != nil {
				if !errors.Is(err, os.ErrNoDeadline) {
					return contextReaderImpl{ctx: ctx, r: r, setupErr: err}
				}
			}
		}
	}
	return contextReaderImpl{ctx: ctx, r: r}
}

type contextReaderImpl struct {
	ctx      context.Context
	r        io.Reader
	setupErr error
}

func (r contextReaderImpl) Read(p []byte) (n int, err error) {
	if r.setupErr != nil {
		return 0, r.setupErr
	}
	if err = r.ctx.Err(); err != nil {
		return
	}
	if n, err = r.r.Read(p); err != nil {
		return
	}
	err = r.ctx.Err()
	return
}

// contextWriter wraps an io.Writer with one that checks ctx.Done() on each Write call.
//
// If ctx has a deadline and if w has a `SetWriteDeadline(time.Time) error` method,
// then it is called with the deadline.
//
// Imported from https://gist.github.com/dchapes/6c992bf3e943934462509338cd213e99
func contextWriter(ctx context.Context, w io.Writer) io.Writer {
	if deadline, ok := ctx.Deadline(); ok {
		type deadliner interface {
			SetWriteDeadline(time.Time) error
		}
		if d, ok := w.(deadliner); ok {
			err := d.SetWriteDeadline(deadline)
			if err != nil {
				if !errors.Is(err, os.ErrNoDeadline) {
					return contextWriterImpl{ctx: ctx, w: w, setupErr: err}
				}
			}
		}
	}
	return contextWriterImpl{ctx: ctx, w: w}
}

type contextWriterImpl struct {
	ctx      context.Context
	w        io.Writer
	setupErr error
}

func (w contextWriterImpl) Write(p []byte) (n int, err error) {
	if w.setupErr != nil {
		return 0, w.setupErr
	}
	if err = w.ctx.Err(); err != nil {
		return
	}
	if n, err = w.w.Write(p); err != nil {
		return
	}
	err = w.ctx.Err()
	return
}
