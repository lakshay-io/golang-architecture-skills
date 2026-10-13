// Package logger builds the process-wide structured logger (log/slog).
//
// Loggers are passed explicitly as dependencies. slog.SetDefault is called
// once in the composition root only so third-party code that logs through
// the default logger comes out in the same format.
package logger

import (
	"context"
	"io"
	"log/slog"

	"example.com/goservice/internal/config"
	"example.com/goservice/internal/platform/requestid"
)

// New builds the logger. wrap, when non-nil, decorates the output handler;
// the composition root uses it to add New Relic logs-in-context.
func New(w io.Writer, cfg config.Config, wrap func(slog.Handler) slog.Handler) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Log.Level}
	var h slog.Handler
	if cfg.Log.Format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	if wrap != nil {
		h = wrap(h)
	}
	return slog.New(contextHandler{h}).With(
		slog.String("service", cfg.ServiceName),
		slog.String("env", cfg.Env),
		slog.String("version", cfg.Version),
	)
}

// contextHandler adds request-scoped attributes (request_id) to every record
// logged with a *Context method, so call sites never have to remember to.
// Always log with InfoContext(ctx, ...) & co. inside a request.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestid.From(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
