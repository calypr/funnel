package tes

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTaskCursorRoundTrip(t *testing.T) {
	token := EncodeTaskCursor("2026-01-01T00:00:00Z", "task-id")
	cursor, legacy, err := DecodeTaskCursor(token)
	if err != nil || legacy || cursor.Created != "2026-01-01T00:00:00Z" || cursor.ID != "task-id" {
		t.Fatalf("got %+v %v %v", cursor, legacy, err)
	}
	cursor, legacy, err = DecodeTaskCursor("old-id")
	if err != nil || !legacy || cursor.ID != "old-id" {
		t.Fatalf("legacy: %+v %v %v", cursor, legacy, err)
	}
	_, _, err = DecodeTaskCursor("task-v1:malformed")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
}
