package config

import (
	"encoding/gob"

	"google.golang.org/protobuf/types/known/structpb"
)

// Both the Funnel server and plugin processes import config, so registering
// protobuf oneof wrappers here keeps net/rpc Config encoding symmetric. Plugin
// binaries must be rebuilt when this config schema changes.
func init() {
	gob.Register(&structpb.Value_NullValue{})
	gob.Register(&structpb.Value_NumberValue{})
	gob.Register(&structpb.Value_StringValue{})
	gob.Register(&structpb.Value_BoolValue{})
	gob.Register(&structpb.Value_StructValue{})
	gob.Register(&structpb.Value_ListValue{})
}
