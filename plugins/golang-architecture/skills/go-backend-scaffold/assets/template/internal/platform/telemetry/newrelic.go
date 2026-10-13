// Package telemetry wires New Relic, the monitoring backend.
//
// This is the only package that configures the agent. Everything else
// reaches the current transaction through the request context
// (newrelic.FromContext) and only at the edges — HTTP middleware, storage
// adapters, outbound clients. Domain code never imports the agent, so the
// business logic stays testable and vendor-neutral.
package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/newrelic/go-agent/v3/integrations/logcontext-v2/nrslog"
	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/config"
)

// statusClientClosedRequest mirrors response.StatusClientClosedRequest
// (platform must not import the transport layer).
const statusClientClosedRequest = 499

// NewRelic builds the agent. When monitoring is disabled (local, tests) it
// still returns a working, inert *Application, so no code path needs an
// "is monitoring on?" branch.
func NewRelic(cfg config.Config, log *slog.Logger) (*newrelic.Application, error) {
	return newrelic.NewApplication(
		// First, so every standard NEW_RELIC_* variable (labels, log
		// forwarding, high security, proxy, profiling...) works without a
		// code change; the typed settings below then take precedence.
		newrelic.ConfigFromEnvironment(),
		newrelic.ConfigEnabled(cfg.NewRelic.Enabled),
		newrelic.ConfigAppName(cfg.NewRelic.AppName),
		newrelic.ConfigLicense(cfg.NewRelic.LicenseKey),
		newrelic.ConfigDistributedTracerEnabled(true),
		newrelic.ConfigLogger(agentLogger{log.With(slog.String("component", "newrelic"))}),
		func(c *newrelic.Config) {
			// A New Relic error means "the server failed, and here is why":
			//   - 4xx are the client's fault: kept visible as EXPECTED errors,
			//     but they do not move the error rate, Apdex or alerts;
			//   - 5xx are not derived from the status code: the HTTP middleware
			//     reports the real Go error (cause chain included) with
			//     NoticeError, instead of a bare "500 Internal Server Error";
			//   - panics are recorded.
			// Every transaction still carries its status code for dashboards.
			c.ErrorCollector.ExpectStatusCodes = statusRange(400, 499)
			c.ErrorCollector.IgnoreStatusCodes = append(c.ErrorCollector.IgnoreStatusCodes, statusRange(500, 599)...)
			c.ErrorCollector.IgnoreStatusCodes = append(c.ErrorCollector.IgnoreStatusCodes, statusClientClosedRequest)
			c.ErrorCollector.RecordPanics = true

			if c.Labels == nil {
				c.Labels = map[string]string{}
			}
			c.Labels["env"] = cfg.Env
			c.Labels["version"] = cfg.Version
		},
	)
}

// LogHandler returns the decorator that adds logs-in-context: log lines
// written with a request context are linked to their trace in New Relic
// (and forwarded, unless NEW_RELIC_APPLICATION_LOGGING_FORWARDING_ENABLED=false
// because logs are already shipped another way).
func LogHandler(app *newrelic.Application) func(slog.Handler) slog.Handler {
	return func(h slog.Handler) slog.Handler { return nrslog.WrapHandler(app, h) }
}

// Shutdown flushes the final harvest so the last minute of data is not lost
// on every deploy. It is bounded by ctx's deadline.
func Shutdown(ctx context.Context, app *newrelic.Application) {
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	app.Shutdown(timeout)
}

func statusRange(from, to int) []int {
	codes := make([]int, 0, to-from+1)
	for c := from; c <= to; c++ {
		codes = append(codes, c)
	}
	return codes
}

// agentLogger sends the agent's own diagnostics through slog, so they come
// out as structured JSON like every other line instead of a second format.
type agentLogger struct{ log *slog.Logger }

func (l agentLogger) Error(msg string, ctx map[string]any) { l.emit(slog.LevelError, msg, ctx) }
func (l agentLogger) Warn(msg string, ctx map[string]any)  { l.emit(slog.LevelWarn, msg, ctx) }
func (l agentLogger) Info(msg string, ctx map[string]any)  { l.emit(slog.LevelInfo, msg, ctx) }
func (l agentLogger) Debug(msg string, ctx map[string]any) { l.emit(slog.LevelDebug, msg, ctx) }

func (l agentLogger) DebugEnabled() bool {
	return l.log.Enabled(context.Background(), slog.LevelDebug)
}

// emit nests the agent's fields under "agent": they include keys such as
// "version" (the agent's) that would otherwise collide with the service's own.
func (l agentLogger) emit(level slog.Level, msg string, fields map[string]any) {
	attrs := make([]slog.Attr, 0, len(fields))
	for k, v := range fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	l.log.LogAttrs(context.Background(), level, msg, slog.Attr{Key: "agent", Value: slog.GroupValue(attrs...)})
}
