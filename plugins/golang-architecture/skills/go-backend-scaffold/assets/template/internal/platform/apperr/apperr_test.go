package apperr

import (
	"errors"
	"fmt"
	"testing"
)

var errThingNotFound = NotFound("thing_not_found", "thing not found")

func TestSentinelSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("load thing 7: %w", errThingNotFound.Wrap(errors.New("no rows")))
	if !errors.Is(err, errThingNotFound) {
		t.Fatal("errors.Is must match the sentinel through wrapping")
	}
	if KindOf(err) != KindNotFound || As(err).Code != "thing_not_found" {
		t.Fatalf("unexpected: %+v", As(err))
	}
}

func TestForeignErrorsAreInternal(t *testing.T) {
	e := As(errors.New("boom"))
	if e.Kind != KindInternal || e.Message != "internal error" {
		t.Fatalf("unexpected: %+v", e)
	}
}

func TestWithDetailsDoesNotMutateSentinel(t *testing.T) {
	_ = errThingNotFound.WithDetails(map[string]string{"id": "bad"})
	if errThingNotFound.Details != nil {
		t.Fatal("sentinel was mutated")
	}
}
