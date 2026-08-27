package worker

import (
	"context"
	"fmt"

	"github.com/ohsu-comp-bio/funnel/tes"
)

// Base64TaskReader reads a task from a base64 encoded string.
type Base64TaskReader struct {
	task *tes.Task
}

// NewBase64TaskReader creates a new Base64TaskReader.
func NewBase64TaskReader(raw string) (*Base64TaskReader, error) {
	task, err := tes.Base64Decode(raw)
	if err != nil {
		return nil, err
	}

	err = tes.InitTask(task, false)
	if err != nil {
		return nil, fmt.Errorf("initializing task: %v", err)
	}

	return &Base64TaskReader{task}, nil
}

// Task returns the task. A random ID will be generated.
func (f *Base64TaskReader) Task(ctx context.Context) (*tes.Task, error) {
	return f.task, nil
}

// State returns the task state. Due to some quirks in the implementation
// of this reader, and since there is no online database to connect to,
// this will always return QUEUED.
func (f *Base64TaskReader) State(ctx context.Context) (tes.State, error) {
	return f.task.GetState(), nil
}

// Close the Base64TaskReader
func (f *Base64TaskReader) Close() {}
