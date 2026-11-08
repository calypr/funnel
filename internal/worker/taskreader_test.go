package worker

import (
	"context"
	"testing"
)

func TestFileTaskReader(t *testing.T) {
	r, err := NewFileTaskReader("../../examples/hello-world.json")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	task, err := r.Task(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task.Name != "Hello world" {
		t.Error("unexpected task content")
	}

	if task.Id == "" {
		t.Error("unexpected empty task ID")
	}
}

func TestBase64TaskReader(t *testing.T) {
	r, err := NewBase64TaskReader("ewogICJuYW1lIjogIkhlbGxvIHdvcmxkIiwKICAiZGVzY3JpcHRpb24iOiAiRGVtb25zdHJhdGVzIHRoZSBtb3N0IGJhc2ljIGVjaG8gdGFzay4iLAogICJleGVjdXRvcnMiOiBbCiAgICB7CiAgICAgICJpbWFnZSI6ICJhbHBpbmUiLAogICAgICAiY29tbWFuZCI6IFsiZWNobyIsICJoZWxsbyB3b3JsZCJdCiAgICB9CiAgXQp9Cg==")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	task, err := r.Task(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task.Name != "Hello world" {
		t.Error("unexpected task content")
	}

	if task.Id == "" {
		t.Error("unexpected empty task ID")
	}
}
