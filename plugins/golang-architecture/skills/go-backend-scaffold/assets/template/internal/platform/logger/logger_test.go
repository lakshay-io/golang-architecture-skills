package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"example.com/goservice/internal/config"
	"example.com/goservice/internal/platform/requestid"
)

func TestRequestIDAddedFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, config.Config{ServiceName: "svc", Env: "test", Log: config.LogConfig{Format: "json"}}, nil)

	log.InfoContext(requestid.With(context.Background(), "req-1"), "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v: %s", err, buf.String())
	}
	if rec["request_id"] != "req-1" || rec["service"] != "svc" {
		t.Fatalf("missing attributes: %v", rec)
	}
}
