// Command openapi writes the service's OpenAPI document to docs/openapi.json
// (`make openapi`), from the handlers' Docs. It needs no configuration.
package main

import (
	"flag"
	"fmt"
	"os"

	"example.com/goservice/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "openapi: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("o", "docs/openapi.json", "the file to write")
	flag.Parse()
	doc, err := app.OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(*out, doc, 0o600)
}
