package events

import (
	"google.golang.org/protobuf/encoding/protojson"
)

// Marshaler provides a default JSON marshaler.
var Marshaler = &protojson.MarshalOptions{
	UseEnumNumbers:  false,
	EmitUnpopulated: false,
	Indent:          "\t",
}

var Unmarshaler = protojson.UnmarshalOptions{
	DiscardUnknown: true,
}

// Marshal marshals the event to JSON.
func Marshal(ev *Event) (string, error) {
	b, err := Marshaler.Marshal(ev)
	return string(b), err
}

// Unmarshal unmarshals the event from JSON.
func Unmarshal(b []byte, ev *Event) error {
	return Unmarshaler.Unmarshal(b, ev)
}
