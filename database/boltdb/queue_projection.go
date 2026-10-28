package boltdb

import (
	"fmt"

	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func taskSchedulingProjection(data []byte) ([]byte, error) {
	return taskWireProjection(data, 1, 4, 9)
}

func taskWireProjection(data []byte, selected ...protowire.Number) ([]byte, error) {
	projected := make([]byte, 0, 128)
	for len(data) > 0 {
		field := data
		number, wireType, tagSize := protowire.ConsumeTag(data)
		if tagSize < 0 {
			return nil, protowire.ParseError(tagSize)
		}
		data = data[tagSize:]
		valueSize := protowire.ConsumeFieldValue(number, wireType, data)
		if valueSize < 0 {
			return nil, protowire.ParseError(valueSize)
		}
		for _, selectedNumber := range selected {
			if number == selectedNumber {
				projected = append(projected, field[:tagSize+valueSize]...)
				break
			}
		}
		data = data[valueSize:]
	}
	return projected, nil
}

func readQueuedTaskProjection(tx *bolt.Tx, id string) (*tes.Task, error) {
	data := tx.Bucket(TaskBucket).Get([]byte(id))
	if data == nil {
		return nil, tes.ErrNotFound
	}
	projected, err := taskSchedulingProjection(data)
	if err != nil {
		return nil, fmt.Errorf("project queued task %q: %w", id, err)
	}
	task := &tes.Task{}
	if err := proto.Unmarshal(projected, task); err != nil {
		return nil, fmt.Errorf("unmarshal queued task projection %q: %w", id, err)
	}
	task.State = getTaskState(tx, id)
	return task, nil
}
