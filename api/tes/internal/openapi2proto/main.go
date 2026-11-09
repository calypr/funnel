package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	serviceOnly := flag.Bool("service", false, "Generate internal TES service bindings instead of public messages")
	flag.Parse()

	input := flag.Arg(0)

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc, err := loader.LoadFromFile(input)
	if err != nil {
		log.Fatalf("Parsing Error: %s\n", err)
	}

	messages, enums, services := parseSchema(doc)

	source, err := renderProto(messages, enums, services)
	if *serviceOnly {
		source, err = renderServiceProto(services)
	}
	if err != nil {
		log.Fatalf("Template Error: %s\n", err)
	}
	fmt.Print(source)
}
