package worker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
)

// FileTaskReader provides a TaskReader implementation from a task file.
type FileTaskReader struct {
	task *tes.Task
}

// NewFileTaskReader creates a new FileTaskReader.
func NewFileTaskReader(path string) (*FileTaskReader, error) {
	// TODO not sure if it's better to return an error immediately,
	//      or return an error from Task()
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening task file: %v", err)
	}
	defer fh.Close()

	jsonData, err := io.ReadAll(fh)
	if err != nil {
		return nil, fmt.Errorf("reading task file: %v", err)
	}

	task := &tes.Task{}
	if err := protojson.Unmarshal(jsonData, task); err != nil {
		return nil, fmt.Errorf("unmarshaling task from JSON: %v", err)
	}

	err = tes.InitTask(task, false)
	if err != nil {
		return nil, fmt.Errorf("initializing task: %v", err)
	}

	return &FileTaskReader{task}, nil
}

// Task returns the task. A random ID will be generated.
func (f *FileTaskReader) Task(ctx context.Context) (*tes.Task, error) {
	return f.task, nil
}

// State returns the task state. Due to some quirks in the implementation
// of this reader, and since there is no online database to connect to,
// this will always return QUEUED.
func (f *FileTaskReader) State(ctx context.Context) (tes.State, error) {
	return f.task.GetState(), nil
}

// Close the FileTaskReader
func (f *FileTaskReader) Close() {}
