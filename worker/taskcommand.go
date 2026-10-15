package worker

import (
	"io"

	"context"

	"github.com/ohsu-comp-bio/funnel/tes"
)

// TaskReader is a type which reads task information
// during task execution.
type TaskReader interface {
	Task(ctx context.Context) (*tes.Task, error)
	State(ctx context.Context) (tes.State, error)
	Close()
}

type TaskCommand interface {
	Run(context.Context) error
	Stop() error
	GetStdout() io.Writer
	GetStderr() io.Writer
	SetStdout(io.Writer)
	SetStderr(io.Writer)
	SetStdin(io.Reader)
}
