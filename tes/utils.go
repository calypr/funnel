package tes

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/getlantern/deepcopy"
	"github.com/rs/xid"
	"google.golang.org/protobuf/encoding/protojson"
)

// Marshaler marshals tasks to indented JSON.
var Marshaler = protojson.MarshalOptions{
	Indent: "  ",
}

// MarshalToString marshals a task to an indented JSON string.
func MarshalToString(t *Task) (string, error) {
	if t == nil {
		return "", fmt.Errorf("can't marshal nil task")
	}
	return Marshaler.Format(t), nil
}

// Base64Encode encodes a task as a base64 encoded string
func Base64Encode(t *Task) (string, error) {
	data, err := Marshaler.Marshal(t)
	if err != nil {
		return "", err
	}
	str := base64.StdEncoding.EncodeToString(data)
	return str, nil
}

// Base64Decode decodes a base64 encoded string into a task
func Base64Decode(raw string) (*Task, error) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding task: %v", err)
	}
	task := &Task{}
	buf := bytes.NewBuffer(data)
	err = protojson.Unmarshal(buf.Bytes(), task)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling task: %v", err)
	}
	return task, nil
}

// ErrNotFound is returned when a task is not found.
var ErrNotFound = errors.New("task not found")
var ErrConcurrentStateChange = errors.New("Concurrent stage change")

// ErrNotPermitted is returned when the owner of a task does not match the
// current non-admin user.
var ErrNotPermitted = errors.New("permission denied")

// Shorthand for task views
const (
	Minimal   = View_MINIMAL
	Basic     = View_BASIC
	Full      = View_FULL
	File      = FileType_FILE
	Directory = FileType_DIRECTORY
)

// GenerateID generates a task ID string.
// IDs are globally unique and sortable.
func GenerateID() string {
	id := xid.New()
	return id.String()
}

// InitTask intializes task fields which are commonly set by CreateTask,
// such as Id, CreationTime, State, etc. If the task fails validation,
// an error is returned. See Validate().
// The given task is modified.
//
// If "overwrite" is true, the fields Id, State, and CreationTime
// will always be overwritten, even if already set, otherwise they
// will only be set if they are empty.
//
// forbiddenPathPrefixes is the configured deny list passed through to Validate.
func InitTask(task *Task, overwrite bool, forbiddenPathPrefixes []string) error {
	if overwrite || task.Id == "" {
		task.Id = GenerateID()
	}
	if overwrite || task.State == Unknown {
		task.State = Queued
	}
	if overwrite || task.CreationTime == "" {
		task.CreationTime = time.Now().Format(time.RFC3339Nano)
	}
	if err := Validate(task, forbiddenPathPrefixes); err != nil {
		return fmt.Errorf("invalid task message:\n%s", err)
	}
	return nil
}

// RunnableState returns true if the state is RUNNING or INITIALIZING
func RunnableState(s State) bool {
	return s == State_INITIALIZING || s == State_RUNNING
}

// TerminalState returns true if the state is COMPLETE, ERROR, SYSTEM_ERROR, or CANCELED
func TerminalState(s State) bool {
	return s == State_COMPLETE || s == State_EXECUTOR_ERROR || s == State_SYSTEM_ERROR ||
		s == State_CANCELED
}

// GetBasicView returns the basic view of a task.
func (task *Task) GetBasicView() *Task {
	view := &Task{}
	deepcopy.Copy(view, task)

	// remove contents from inputs
	for _, v := range view.Inputs {
		v.Content = ""
	}

	// remove stdout and stderr from Task.Logs.Logs
	for _, tl := range view.Logs {
		tl.SystemLogs = nil
		for _, el := range tl.Logs {
			el.Stdout = ""
			el.Stderr = ""
		}
	}
	return view
}

// GetMinimalView returns the minimal view of a task.
func (task *Task) GetMinimalView() *Task {
	id := task.Id
	state := task.State
	return &Task{
		Id:    id,
		State: state,
	}
}

// GetTaskLog gets the task log entry at the given index "i".
// If the entry doesn't exist, empty logs will be appended up to "i".
func (task *Task) GetTaskLog(i int) *TaskLog {

	// Grow slice length if necessary
	for j := len(task.Logs); j <= i; j++ {
		task.Logs = append(task.Logs, &TaskLog{})
	}

	return task.Logs[i]
}

// GetExecLog gets the executor log entry at the given index "i".
// If the entry doesn't exist, empty logs will be appended up to "i".
func (task *Task) GetExecLog(attempt int, i int) *ExecutorLog {
	tl := task.GetTaskLog(attempt)

	// Grow slice length if necessary
	for j := len(tl.Logs); j <= i; j++ {
		tl.Logs = append(tl.Logs, &ExecutorLog{})
	}

	return tl.Logs[i]
}

// GetPageSize takes in the page size from a request and returns a new page size
// taking into account the minimum, maximum and default as documented in the TES spec.
func GetPageSize(reqSize int32) int {
	// default page size
	var pageSize = 256

	if reqSize > 0 {
		pageSize = int(reqSize)

		// max page size
		if pageSize > 2048 {
			pageSize = 2048
		}
	}

	return pageSize
}
