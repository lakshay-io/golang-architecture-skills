// Package middleware holds the Gin middleware every service runs. Each one
// does a single job; api.NewRouter decides the order.
//
// gin.Default() is deliberately not used: its Logger and Recovery print
// unstructured text and know nothing about request ids or our response body.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/platform/requestid"
)

// RequestID trusts a well-formed inbound X-Request-ID (set by the gateway) or
// mints one, stores it in the request context — where the logger and outbound
// clients pick it up — and echoes it on the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		c.Header(requestid.Header, id)
		c.Request = c.Request.WithContext(requestid.With(c.Request.Context(), id))
		c.Next()
	}
}

// AccessLog writes exactly one structured line per request, including the
// error cause recorded by response.Error. It logs the route TEMPLATE
// (c.FullPath(), e.g. "/v1/items/:id"), not the raw path, so the field has
// bounded cardinality and can double as a metrics label.
//
// skipRoutes (health probes) are not logged: probes hit every pod every few
// seconds and would drown real traffic.
func AccessLog(log *slog.Logger, skipRoutes ...string) gin.HandlerFunc {
	skip := make(map[string]struct{}, len(skipRoutes))
	for _, r := range skipRoutes {
		skip[r] = struct{}{}
	}
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if _, ok := skip[route]; ok {
			return
		}
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", status),
			slog.Int("bytes", max(c.Writer.Size(), 0)),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("client_ip", c.ClientIP()),
		}
		if last := c.Errors.Last(); last != nil {
			attrs = append(attrs, slog.String("err", last.Err.Error()))
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		log.LogAttrs(c.Request.Context(), level, "http request", attrs...)
	}
}

// Recovery turns a panic in one request into a 500 instead of crashing the
// process and every other in-flight request with it.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok {
				if errors.Is(err, http.ErrAbortHandler) {
					panic(v) // deliberate abort: net/http handles it silently
				}
				if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					_ = c.Error(err) // client is gone; nothing to write to
					c.Abort()
					return
				}
			}
			log.ErrorContext(c.Request.Context(), "panic recovered",
				slog.Any("panic", v),
				slog.String("stack", string(debug.Stack())),
			)
			response.Error(c, fmt.Errorf("panic: %v", v))
		}()
		c.Next()
	}
}

// Timeout gives each request a context deadline. It is cooperative: handlers
// must pass c.Request.Context() to the service, and the service down to every
// DB query and outbound call, so the deadline actually cancels work instead of
// leaving orphaned goroutines behind a client that already gave up.
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// BodyLimit caps request bodies. A declared Content-Length over the limit is
// rejected before reading a byte; chunked bodies are cut off by
// http.MaxBytesReader and surface as 413 from the bind step.
func BodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			response.Error(c, &response.HTTPError{
				Status:  http.StatusRequestEntityTooLarge,
				Code:    "body_too_large",
				Message: fmt.Sprintf("request body must not exceed %d bytes", limit),
			})
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// SecureHeaders sets the response headers the OWASP REST Security Cheat
// Sheet asks of a JSON API (docs/SECURITY.md). A JSON body is never a page:
// the CSP forbids loading or framing anything from it, a browser must not
// guess another content type, nothing is cached, and once a client has seen
// the service over HTTPS it never falls back to plain HTTP (HSTS is ignored
// on a plain-HTTP response, so local runs are unaffected).
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}
