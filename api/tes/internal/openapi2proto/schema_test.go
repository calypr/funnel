package main

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOutputTypeGeneratesProto3Optional(t *testing.T) {
	typeSchema := &openapi3.SchemaRef{
		Ref: "#/components/schemas/tesFileType",
		Value: &openapi3.Schema{
			Type: &openapi3.Types{},
		},
	}
	schema := &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Properties: map[string]*openapi3.SchemaRef{"type": typeSchema},
		},
	}

	output, err := parseMessageSchema("tesOutput", schema, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	input, err := parseMessageSchema("tesInput", schema, "FULL")
	if err != nil {
		t.Fatal(err)
	}

	messages := []Message{output, input}
	cleanSchema(messages, nil, nil)
	source, err := renderProto(messages, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(source, "optional FileType type = 1;") {
		t.Fatalf("generated proto does not make Output.type optional:\n%s", source)
	}
	if strings.Contains(source, "optional FileType type = 1;") && strings.Count(source, "optional FileType type = 1;") != 1 {
		t.Fatalf("generated proto makes more than Output.type optional:\n%s", source)
	}
}

func TestMessagesAndServiceGenerateSeparateGoPackages(t *testing.T) {
	models, err := renderProto([]Message{{Name: "Task"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := renderServiceProto([]ServicePath{{Name: "CreateTask", Path: "/tasks", Mode: "post", InputType: "Task", OutputType: "Task"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(models, "service TaskService") || strings.Contains(models, "google/api") {
		t.Fatal("public models contain server bindings")
	}
	for _, required := range []string{`package tes;`, `github.com/calypr/funnel/internal/tesrpc`, `import "tes/tes.proto";`, `rpc CreateTask(Task) returns (Task)`, `/ga4gh/tes/v1/tasks`} {
		if !strings.Contains(service, required) {
			t.Fatalf("service missing %q", required)
		}
	}
}
