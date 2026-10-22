package app_test

import (
	"bytes"
	"os"
	"testing"

	"example.com/goservice/internal/app"
)

// docs/openapi.json is what `make openapi` writes from the handlers' Docs. A
// change to a route or a DTO without `make openapi` fails here, so every
// branch carries the spec of its own code.
func TestOpenAPIIsCurrent(t *testing.T) {
	want, err := app.OpenAPI()
	if err != nil {
		t.Fatalf("build the spec: %v", err)
	}
	got, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatalf("read docs/openapi.json (run make openapi): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/openapi.json is stale: run make openapi and commit it")
	}
}
