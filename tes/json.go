package tes

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
)

// Marshaler marshals TES messages to indented JSON.
var Marshaler = protojson.MarshalOptions{Indent: "  "}

// MarshalToString marshals a task to indented JSON.
func MarshalToString(t *Task) (string, error) {
	if t == nil {
		return "", fmt.Errorf("can't marshal nil task")
	}
	return Marshaler.Format(t), nil
}
