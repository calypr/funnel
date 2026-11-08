package taskmodel

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/calypr/funnel/api/tes"
)

// Base64Encode encodes a task as a base64 encoded string
func Base64Encode(t *tes.Task) (string, error) {
	data, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(t)
	if err != nil {
		return "", err
	}
	str := base64.StdEncoding.EncodeToString(data)
	return str, nil
}

// Base64Decode decodes a base64 encoded string into a task
func Base64Decode(raw string) (*tes.Task, error) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding task: %v", err)
	}
	task := &tes.Task{}
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

// GenerateID generates a task ID string.
// IDs are globally unique and sortable.
func GenerateID() string {
	id, err := uuid.NewUUID()
	if err != nil {
		// fall back to the random v4 UUID if the time-based one fails
		return uuid.NewString()
	}
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
func InitTask(task *tes.Task, overwrite bool) error {
	return InitTaskWithForbiddenPathPrefixes(task, overwrite, nil)
}

// InitTaskWithForbiddenPathPrefixes initializes and validates a task while
// rejecting mounts at or beneath the configured container path prefixes.
func InitTaskWithForbiddenPathPrefixes(task *tes.Task, overwrite bool, forbiddenPathPrefixes []string) error {
	if overwrite || task.Id == "" {
		task.Id = GenerateID()
	}
	if overwrite || task.State == tes.Unknown {
		task.State = tes.Queued
	}
	if overwrite || task.CreationTime == "" {
		task.CreationTime = time.Now().Format(time.RFC3339Nano)
	}
	if err := ValidateWithForbiddenPathPrefixes(task, forbiddenPathPrefixes); err != nil {
		return fmt.Errorf("invalid task message:\n%s", err)
	}
	return nil
}

// GetBasicView returns the basic view of a task.
func GetBasicView(task *tes.Task) *tes.Task {
	view := proto.Clone(task).(*tes.Task)

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
func GetMinimalView(task *tes.Task) *tes.Task {
	id := task.Id
	state := task.State
	return &tes.Task{
		Id:    id,
		State: state,
	}
}

// GetTaskLog gets the task log entry at the given index "i".
// If the entry doesn't exist, empty logs will be appended up to "i".
func GetTaskLog(task *tes.Task, i int) *tes.TaskLog {

	// Grow slice length if necessary
	for j := len(task.Logs); j <= i; j++ {
		task.Logs = append(task.Logs, &tes.TaskLog{})
	}

	return task.Logs[i]
}

// GetExecLog gets the executor log entry at the given index "i".
// If the entry doesn't exist, empty logs will be appended up to "i".
func GetExecLog(task *tes.Task, attempt int, i int) *tes.ExecutorLog {
	tl := GetTaskLog(task, attempt)

	// Grow slice length if necessary
	for j := len(tl.Logs); j <= i; j++ {
		tl.Logs = append(tl.Logs, &tes.ExecutorLog{})
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
