package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi3"
)

type Field struct {
	Name     string
	Type     string
	Repeated bool
	ID       int
	Source   *openapi3.SchemaRef
}

type Message struct {
	Name   string
	Fields []Field
}

type Enum struct {
	Name   string
	Values []interface{}
}

type ServicePath struct {
	Name       string
	Path       string
	Mode       string
	InputType  string
	OutputType string
}

func getType(p *openapi3.SchemaRef) (bool, string) {
	if p.Value.Type.Includes("integer") {
		return false, "int32"
	}
	if p.Value.Type.Includes("boolean") {
		return false, "bool"
	}
	if p.Value.Type.Includes("number") {
		return false, "double"
	}
	if p.Value.Type.Includes("object") {
		if p.Ref != "" {
			t := strings.Split(p.Ref, "/")
			return false, t[len(t)-1]
		}
		if p.Value.AdditionalProperties.Has != nil && *p.Value.AdditionalProperties.Has != false {
			_, aType := getType(p.Value.AdditionalProperties.Schema)
			return false, fmt.Sprintf("map<string,%s>", aType)
		}
		return false, "map<string,string>"
	}
	if p.Value.Type.Includes("array") {
		if p.Value.Items.Ref != "" {
			t := strings.Split(p.Value.Items.Ref, "/")
			return true, t[len(t)-1]
		}
		_, aType := getType(p.Value.Items)
		return true, aType
	}
	if p.Ref != "" {
		t := strings.Split(p.Ref, "/")
		return false, t[len(t)-1]
	}
	return false, p.Value.Type.Slice()[0]
}

func getParamType(param *openapi3.Parameter) (bool, string) {
	if param.Schema.Ref != "" {
		t := strings.Split(param.Schema.Ref, "/")
		return false, t[len(t)-1]
	}
	if param.Schema.Value.Type != nil && param.Schema.Value.Type.Includes("") == false {
		return getType(param.Schema)
	}
	return false, ""
}

func getFields(schema *openapi3.SchemaRef) []Field {
	fields := []Field{}
	for pname, pschema := range schema.Value.Properties {
		repeated, fType := getType(pschema)
		f := Field{Name: pname, Repeated: repeated, Type: fType, Source: schema}
		fields = append(fields, f)
	}
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	for i := range fields {
		fields[i].ID = i + 1
	}
	return fields
}

func getFieldsWithView(schema *openapi3.SchemaRef, view string) []Field {
	fields := getFields(schema)
	if view == "MINIMAL" && schema.Ref != "" && strings.HasSuffix(schema.Ref, "tesTask") {
		minFields := []Field{}
		for _, f := range fields {
			if f.Name == "id" || f.Name == "state" {
				minFields = append(minFields, f)
			}
		}
		return minFields
	}
	if view == "BASIC" {
		basicFields := []Field{}
		for _, f := range fields {
			if schema.Ref != "" && strings.HasSuffix(schema.Ref, "tesExecutorLog") {
				if f.Name == "stdout" || f.Name == "stderr" {
					continue
				}
			}
			if schema.Ref != "" && strings.HasSuffix(schema.Ref, "tesInput") {
				if f.Name == "content" {
					continue
				}
			}
			if schema.Ref != "" && strings.HasSuffix(schema.Ref, "tesTaskLog") {
				if f.Name == "system_logs" {
					continue
				}
			}
			if schema.Ref != "" && strings.HasSuffix(schema.Ref, "tesExecutor") {
				if f.Name == "stdout" || f.Name == "stderr" {
					continue
				}
			}
			basicFields = append(basicFields, f)
		}
		return basicFields
	}
	return fields
}

func parseMessageSchema(name string, schema *openapi3.SchemaRef, view string) (Message, error) {
	if schema.Value.Properties != nil {
		fields := getFieldsWithView(schema, view)
		m := Message{Name: name, Fields: fields}
		return m, nil
	} else if schema.Value.AllOf != nil {
		fieldMap := map[string]Field{}
		for i := range schema.Value.AllOf {
			newFields := getFieldsWithView(schema.Value.AllOf[i], view)
			for _, f := range newFields {
				if x, ok := fieldMap[f.Name]; !ok {
					fieldMap[f.Name] = f
				} else {
					if x.Source.Ref != "" && f.Source.Ref == "" {
						fieldMap[f.Name] = f
					}
				}
			}
		}
		fields := []Field{}
		for _, v := range fieldMap {
			fields = append(fields, v)
		}
		sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		for i := range fields {
			fields[i].ID = i + 1
		}
		m := Message{Name: name, Fields: fields}
		return m, nil
	} else if schema.Value.Enum != nil {
		return Message{}, fmt.Errorf("Message is Enum")
	} else {
		m := Message{Name: name, Fields: []Field{}}
		return m, nil
	}
}

func parseMessageEnum(name string, schema *openapi3.SchemaRef) (Enum, error) {
	e := Enum{Name: name, Values: schema.Value.Enum}
	return e, nil
}

func cleanSchema(messages []Message, enums []Enum, services []ServicePath) {
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Name < messages[j].Name })

	var prefixRemove = "tes"
	for i := range messages {
		messages[i].Name = strings.TrimPrefix(messages[i].Name, prefixRemove)
		for j := range messages[i].Fields {
			messages[i].Fields[j].Type = strings.TrimPrefix(messages[i].Fields[j].Type, prefixRemove)
		}
	}

	for i := range enums {
		enums[i].Name = strings.TrimPrefix(enums[i].Name, prefixRemove)
	}

	for i := range services {
		services[i].InputType = strings.TrimPrefix(services[i].InputType, prefixRemove)
		services[i].OutputType = strings.TrimPrefix(services[i].OutputType, prefixRemove)
	}
}

func getResponseMessage(resp *openapi3.Responses) string {
	schema := resp.Status(200).Value.Content.Get("application/json").Schema
	s := strings.Split(schema.Ref, "/")
	return s[len(s)-1]
}

func adjustListTasksResponseFields(fields []Field, taskType string) []Field {
	adjusted := []Field{}
	for _, f := range fields {
		if f.Name == "tasks" {
			f.Type = taskType
		}
		adjusted = append(adjusted, f)
	}
	return adjusted
}

func main() {
	flag.Parse()

	input := flag.Arg(0)

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc, err := loader.LoadFromFile(input)
	if err != nil {
		log.Fatalf("Parsing Error: %s\n", err)
	}

	messages := []Message{}
	enums := []Enum{}
	services := []ServicePath{}

	for name, schema := range doc.Components.Schemas {
		if schema.Value.Enum != nil {
			if e, err := parseMessageEnum(name, schema); err == nil {
				enums = append(enums, e)
			}
		} else {
			if m, err := parseMessageSchema(name, schema, "FULL"); err == nil {
				messages = append(messages, m)
			}
			if strings.HasSuffix(name, "tesTask") {
				if m, err := parseMessageSchema("TaskMin", schema, "MINIMAL"); err == nil {
					messages = append(messages, m)
				}
				if m, err := parseMessageSchema("TaskBasic", schema, "BASIC"); err == nil {
					for i, f := range m.Fields {
						if f.Name == "executors" {
							m.Fields[i].Type = "ExecutorBasic"
						}
						if f.Name == "inputs" {
							m.Fields[i].Type = "InputBasic"
						}
						if f.Name == "logs" {
							m.Fields[i].Type = "TaskLogBasic"
						}
					}
					messages = append(messages, m)
				}
			}
			if strings.HasSuffix(name, "tesExecutor") {
				if m, err := parseMessageSchema("ExecutorBasic", schema, "BASIC"); err == nil {
					messages = append(messages, m)
				}
			}
			if strings.HasSuffix(name, "tesInput") {
				if m, err := parseMessageSchema("InputBasic", schema, "BASIC"); err == nil {
					messages = append(messages, m)
				}
			}
			if strings.HasSuffix(name, "tesTaskLog") {
				if m, err := parseMessageSchema("TaskLogBasic", schema, "BASIC"); err == nil {
					messages = append(messages, m)
				}
			}
			if strings.HasSuffix(name, "tesListTasksResponse") {
				if m, err := parseMessageSchema("ListTasksResponseMin", schema, "MINIMAL"); err == nil {
					m.Fields = adjustListTasksResponseFields(m.Fields, "TaskMin")
					messages = append(messages, m)
				}
				if m, err := parseMessageSchema("ListTasksResponseBasic", schema, "BASIC"); err == nil {
					m.Fields = adjustListTasksResponseFields(m.Fields, "TaskBasic")
					messages = append(messages, m)
				}
			}
		}
	}

	for name, param := range doc.Components.Parameters {
		if param.Value.Schema.Value.Enum != nil {
			if e, err := parseMessageEnum(name, param.Value.Schema); err == nil {
				enums = append(enums, e)
			}
		}
	}

	for _, req := range doc.Paths.Map() {
		if req.Get != nil {
			reqFields := []Field{}
			for _, param := range req.Get.Parameters {
				r, t := getParamType(param.Value)
				reqFields = append(reqFields, Field{Name: param.Value.Name, Type: t, Repeated: r})
			}
			for i := range reqFields {
				reqFields[i].ID = i + 1
			}
			m := Message{Name: req.Get.OperationID + "Request", Fields: reqFields}
			messages = append(messages, m)
		}
		if req.Post != nil {
			reqFields := []Field{}
			for _, param := range req.Post.Parameters {
				r, t := getParamType(param.Value)
				reqFields = append(reqFields, Field{Name: param.Value.Name, Type: t, Repeated: r})
			}
			for i := range reqFields {
				reqFields[i].ID = i + 1
			}
			if req.Post.RequestBody != nil {
			} else {
				m := Message{Name: req.Post.OperationID + "Request", Fields: reqFields}
				messages = append(messages, m)
			}
		}
	}

	for path, req := range doc.Paths.Map() {
		p := ServicePath{}
		if req.Get != nil {
			p.Name = req.Get.OperationID
			p.Path = path
			p.Mode = "get"
			p.InputType = req.Get.OperationID + "Request"
			p.OutputType = getResponseMessage(req.Get.Responses)
			services = append(services, p)
		}
		if req.Post != nil {
			p.Name = req.Post.OperationID
			p.Path = path
			p.Mode = "post"
			if req.Post.RequestBody != nil {
				s := req.Post.RequestBody.Value.Content.Get("application/json").Schema.Ref
				sL := strings.Split(s, "/")
				p.InputType = sL[len(sL)-1]
			} else {
				p.InputType = req.Post.OperationID + "Request"
			}
			p.OutputType = getResponseMessage(req.Post.Responses)
			services = append(services, p)
		}
	}

	cleanSchema(messages, enums, services)

	tmpl, err := template.New("proto").Parse(`
syntax = "proto3";

option go_package = "github.com/ohsu-comp-bio/funnel/tes";

package tes;

import "google/api/annotations.proto";

{{range $i, $enum := .enums}}
enum {{$enum.Name}} { {{range $j, $value := $enum.Values}}
	{{$value}} = {{$j}};{{end}}
}
{{end}}
{{range $i, $message := .messages}}
message {{$message.Name}} { {{range $j, $field := $message.Fields}}
	{{if $field.Repeated}}repeated {{end}}{{$field.Type}} {{$field.Name}} = {{$field.ID}};{{end}}
}
{{end}}

service TaskService {
{{range $i, $path := .services}}
    rpc {{$path.Name}}({{$path.InputType}}) returns ({{$path.OutputType}}) {
      option (google.api.http) = {
        {{$path.Mode}}: "{{$path.Path}}"
		additional_bindings {
			{{$path.Mode}}: "/v1{{$path.Path}}"
			{{- if eq $path.Mode "post"}}
			body: "*"{{end}}
		}
		additional_bindings {
			{{$path.Mode}}: "/ga4gh/tes/v1{{$path.Path}}"
			{{- if eq $path.Mode "post"}}
			body: "*"{{end}}
		}
		{{- if eq $path.Mode "post"}}
		body: "*"{{end}}
      };
    }
{{end}}
}

`)
	if err != nil {
		log.Fatalf("Template Error: %s\n", err)
	}

	err = tmpl.Execute(os.Stdout, map[string]interface{}{"messages": messages, "enums": enums, "services": services})
	if err != nil {
		log.Fatalf("Template Execution Error: %s", err)
	}
}
