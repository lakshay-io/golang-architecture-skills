package telemetry

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/config"
)

func TestDisabledAgentIsInert(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewRelic(config.Config{
		Env:      config.EnvTest,
		NewRelic: config.NewRelicConfig{Enabled: false, AppName: "svc-test"},
	}, log)
	if err != nil {
		t.Fatalf("disabled agent must start without a license: %v", err)
	}

	// The agent must be safe to use exactly as in production.
	txn := app.StartTransaction("GET /v1/things")
	ctx := newrelic.NewContext(context.Background(), txn)
	slog.New(LogHandler(app)(slog.NewJSONHandler(io.Discard, nil))).InfoContext(ctx, "inside txn")
	txn.NoticeError(io.ErrUnexpectedEOF)
	txn.End()
	Shutdown(context.Background(), app)

	cfg, _ := app.Config()
	if !slices.Contains(cfg.ErrorCollector.IgnoreStatusCodes, 500) || !slices.Contains(cfg.ErrorCollector.ExpectStatusCodes, 400) {
		t.Fatalf("error collector policy not applied: %+v", cfg.ErrorCollector)
	}
}
