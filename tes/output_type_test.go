package tes

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestOutputTypeJSONPresence(t *testing.T) {
	typeField := (&Output{}).ProtoReflect().Descriptor().Fields().ByName("type")
	tests := []struct {
		name     string
		json     string
		wantType FileType
		wantSet  bool
	}{
		{name: "omitted", json: `{}`, wantType: FileType_FILE},
		{name: "null", json: `{"type":null}`, wantType: FileType_FILE},
		{name: "explicit file", json: `{"type":"FILE"}`, wantType: FileType_FILE, wantSet: true},
		{name: "explicit directory", json: `{"type":"DIRECTORY"}`, wantType: FileType_DIRECTORY, wantSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output Output
			if err := protojson.Unmarshal([]byte(tt.json), &output); err != nil {
				t.Fatal(err)
			}

			gotSet := output.ProtoReflect().Has(typeField)
			if gotSet != tt.wantSet {
				t.Fatalf("type presence = %t, want %t", gotSet, tt.wantSet)
			}
			if output.GetType() != tt.wantType {
				t.Fatalf("type = %s, want %s", output.GetType(), tt.wantType)
			}

			data, err := proto.Marshal(&output)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Output
			if err := proto.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if gotSet := decoded.Type != nil; gotSet != tt.wantSet {
				t.Fatalf("binary round-trip type presence = %t, want %t", gotSet, tt.wantSet)
			}
			if decoded.GetType() != tt.wantType {
				t.Fatalf("binary round-trip type = %s, want %s", decoded.GetType(), tt.wantType)
			}
		})
	}
}
