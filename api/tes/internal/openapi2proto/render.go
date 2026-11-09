package main

import (
	"strings"
	"text/template"
)

func renderProto(messages []Message, enums []Enum, services []ServicePath) (string, error) {
	tmpl, err := template.New("proto").Parse(`
syntax = "proto3";

option go_package = "github.com/calypr/funnel/api/tes";

package tes;


{{range $i, $enum := .enums}}
enum {{$enum.Name}} { {{range $j, $value := $enum.Values}}
	{{$value}} = {{$j}};{{end}}
}
{{end}}
{{range $i, $message := .messages}}
message {{$message.Name}} { {{range $j, $field := $message.Fields}}
	{{if $field.Repeated}}repeated {{else if $field.Optional}}optional {{end}}{{$field.Type}} {{$field.Name}} = {{$field.ID}};{{end}}
}
{{end}}



`)
	if err != nil {
		return "", err
	}

	var source strings.Builder
	err = tmpl.Execute(&source, map[string]interface{}{"messages": messages, "enums": enums, "services": services})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(source.String()) + "\n", nil
}

func renderServiceProto(services []ServicePath) (string, error) {
	tmpl, err := template.New("service").Parse(`syntax = "proto3";
package tes;
option go_package = "github.com/calypr/funnel/internal/tesrpc";
import "tes/tes.proto";
import "google/api/annotations.proto";

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
		return "", err
	}
	var source strings.Builder
	if err := tmpl.Execute(&source, map[string]interface{}{"services": services}); err != nil {
		return "", err
	}
	return strings.TrimSpace(source.String()) + "\n", nil
}
