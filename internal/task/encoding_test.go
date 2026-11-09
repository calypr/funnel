package task

import (
	"reflect"
	"testing"

	"github.com/calypr/funnel/api/tes"
)

func TestBase64Encode(t *testing.T) {
	task := &tes.Task{
		Id: "task1",
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello world"},
			},
		},
	}

	expected := "ewogICJleGVjdXRvcnMiOiAgWwogICAgewogICAgICAiY29tbWFuZCI6ICBbCiAgICAgICAgImVjaG8iLAogICAgICAgICJoZWxsbyB3b3JsZCIKICAgICAgXSwKICAgICAgImltYWdlIjogICJhbHBpbmUiCiAgICB9CiAgXSwKICAiaWQiOiAgInRhc2sxIgp9"

	// TODO: Odd (and as of yet unexpained!) encoding/decoding issue with two spaces after field (e.g. `"id":  "task1"`)?
	expected_alt := "ewogICJleGVjdXRvcnMiOiBbCiAgICB7CiAgICAgICJjb21tYW5kIjogWwogICAgICAgICJlY2hvIiwKICAgICAgICAiaGVsbG8gd29ybGQiCiAgICAgIF0sCiAgICAgICJpbWFnZSI6ICJhbHBpbmUiCiAgICB9CiAgXSwKICAiaWQiOiAidGFzazEiCn0="

	encoded, err := Base64Encode(task)
	if err != nil {
		t.Fatal(err)
	}

	if encoded != expected && encoded != expected_alt {
		t.Logf("expected: %+v", expected)
		t.Logf("expected_alt: %+v", expected_alt)
		t.Logf("actual: %+v", encoded)
		t.Fatal("unexpected value returned from Base64Encode")
	}

	decoded, err := Base64Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(decoded, task) {
		t.Logf("expected: %+v", task)
		t.Logf("actual: %+v", decoded)
		t.Fatal("incorrect decoded task from Base64Decode")
	}
}
