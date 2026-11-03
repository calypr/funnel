package tes

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TaskCursor matches descending creation-time and ID ordering.
type TaskCursor struct {
	Created string
	ID      string
}

const taskCursorPrefix = "task-v1:"

func EncodeTaskCursor(created, id string) string {
	data, _ := json.Marshal(TaskCursor{Created: created, ID: id}) //nolint:errcheck // This value contains only strings, which encoding/json always supports.
	return taskCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

// DecodeTaskCursor also accepts legacy ID-only tokens, which backends resolve to timestamps.
func DecodeTaskCursor(token string) (TaskCursor, bool, error) {
	if !strings.HasPrefix(token, taskCursorPrefix) {
		return TaskCursor{ID: token}, true, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, taskCursorPrefix))
	var cursor TaskCursor
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.ID == "" {
		return cursor, false, status.Error(codes.InvalidArgument, "invalid task page token")
	}
	return cursor, false, nil
}
