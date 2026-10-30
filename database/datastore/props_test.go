package datastore

import (
	"context"
	"testing"

	"cloud.google.com/go/datastore"
	"github.com/calypr/funnel/tes"
)

func TestTaskDatastoreRoundTripPreservesOutputTypePresence(t *testing.T) {
	wantTypes := []*tes.FileType{nil, tes.FileType_FILE.Enum(), tes.FileType_DIRECTORY.Enum()}
	sourceTask := &tes.Task{
		Id:      "presence-round-trip",
		Inputs:  []*tes.Input{{Path: "/input"}},
		Outputs: []*tes.Output{{Path: "/outputs/omitted"}, {Path: "/outputs/file", Type: wantTypes[1]}, {Path: "/outputs/directory", Type: wantTypes[2]}},
	}

	_, values := marshalTask(sourceTask, context.Background())
	properties, err := datastore.SaveStruct(values[0])
	if err != nil {
		t.Fatal(err)
	}
	var stored task
	if err := datastore.LoadStruct(&stored, properties); err != nil {
		t.Fatal(err)
	}

	got := stored.unmarshal()
	if got.Inputs[0].GetType() != tes.FileType_FILE {
		t.Fatalf("input default type = %s, want FILE", got.Inputs[0].GetType())
	}
	for i, wantType := range wantTypes {
		gotType := got.Outputs[i].Type
		if (gotType != nil) != (wantType != nil) {
			t.Errorf("output %d type presence = %t, want %t", i, gotType != nil, wantType != nil)
			continue
		}
		if got.Outputs[i].GetType() != tes.FileType_FILE && wantType == nil {
			t.Errorf("output %d omitted type default = %s, want FILE", i, got.Outputs[i].GetType())
		}
		if wantType != nil && *gotType != *wantType {
			t.Errorf("output %d type = %s, want %s", i, *gotType, *wantType)
		}
	}
}

func TestDatastoreLegacyOutputWithoutTypeStaysOmitted(t *testing.T) {
	legacy := param{Path: "/outputs/legacy"}
	properties, err := datastore.SaveStruct(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range properties {
		if property.Name == "Type" {
			t.Fatal("legacy output with no type unexpectedly stored a Type property")
		}
	}

	var stored param
	if err := datastore.LoadStruct(&stored, properties); err != nil {
		t.Fatal(err)
	}
	if stored.Type != nil {
		t.Fatalf("legacy output type = %d, want omitted", *stored.Type)
	}
	output := (&task{Outputs: []param{stored}}).unmarshal().Outputs[0]
	if output.Type != nil {
		t.Fatalf("unmarshaled legacy output type = %s, want omitted", *output.Type)
	}
}
