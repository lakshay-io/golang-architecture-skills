// Package response renders every API response, success or failure, in one
// body shape, the same in every CyberTot Go service, so a client parses them
// all the same way:
//
//	{"success": true,  "status": 202, "code": "batch_accepted",    "message": "...", "data": {...},    "requestId": "..."}
//	{"success": false, "status": 400, "code": "telemetry_invalid", "message": "...", "details": {...}, "requestId": "..."}
//
// success, status, code, message and requestId are on every response. data is
// on every success and never on a failure; details only on a failure that
// names fields. Handlers render a success with Success and `return err` for
// a failure, which Wrap sends to Error; nothing writes a body by hand.
package response

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/platform/apperr"
	"example.com/goservice/internal/platform/requestid"
)

// StatusClientClosedRequest is nginx's non-standard 499: the client went away
// before we answered. Nobody reads the body; it only keeps metrics honest.
const StatusClientClosedRequest = 499

// Body is every response body the API writes. The doc tags describe its
// fields in the API docs (internal/api/apidoc).
type Body struct {
	// Success is true for 2xx, false otherwise.
	Success bool `json:"success" doc:"True for a 2xx, false otherwise."`
	// Status repeats the HTTP status, for clients that only keep the body.
	Status int `json:"status" doc:"The HTTP status, repeated."`
	// Code is the stable, machine-readable outcome clients switch on, e.g.
	// "batch_accepted" or "telemetry_invalid".
	Code string `json:"code" doc:"The outcome, stable: switch on it."`
	// Message is a human-readable sentence; never parse it.
	Message string `json:"message" doc:"A sentence for logs or people. Never parse it."`
	// Data is the result: on every success, never on a failure.
	Data any `json:"data,omitempty" doc:"The result: on every success, never on a failure."`
	// Details names each invalid field and what is wrong with it: only on a
	// failure that is about specific fields.
	Details map[string]string `json:"details,omitempty" doc:"Field path to problem: only on a failure about specific fields."`
	// RequestID matches the X-Request-ID header, the logs and New Relic.
	RequestID string `json:"requestId" doc:"Equal to the X-Request-ID header. Quote it when reporting a problem."`
}

// HTTPError is a transport-level failure with an explicit status: things the
// domain never says, such as 404 for an unknown route, 405, 413 or 429.
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// Success renders a 2xx. code names the outcome ("safezone_created") and
// data is the result; a nil data is rendered as {} so that every success
// carries data.
func Success(c *gin.Context, status int, code, message string, data any) {
	if data == nil {
		data = struct{}{}
	}
	c.JSON(status, Body{
		Success:   true,
		Status:    status,
		Code:      code,
		Message:   message,
		Data:      data,
		RequestID: requestid.From(c.Request.Context()),
	})
}

// StatusFor maps a domain error kind to its HTTP status.
func StatusFor(k apperr.Kind) int {
	switch k {
	case apperr.KindInvalid:
		return http.StatusBadRequest
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindUnauthorized:
		return http.StatusUnauthorized
	case apperr.KindForbidden:
		return http.StatusForbidden
	case apperr.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Error renders err as a failure body and aborts the chain.
//
// The full error (with its wrapped cause chain) is attached to the gin context
// so the access log records it; clients only ever see the safe Message.
func Error(c *gin.Context, err error) {
	_ = c.Error(err)

	if he, ok := errors.AsType[*HTTPError](err); ok {
		abort(c, he.Status, he.Code, he.Message, nil)
		return
	}
	// Deadlines and disconnects are not application bugs: give them their own
	// statuses so they never inflate the 5xx error-rate alert.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		abort(c, http.StatusGatewayTimeout, "timeout", "request timed out", nil)
		return
	case errors.Is(err, context.Canceled):
		c.AbortWithStatus(StatusClientClosedRequest)
		return
	}

	e := apperr.As(err)
	msg := e.Message
	if e.Kind == apperr.KindInternal {
		msg = "internal error" // never leak internals (SQL, hostnames, stack) to clients
	}
	abort(c, StatusFor(e.Kind), e.Code, msg, e.Details)
}

func abort(c *gin.Context, status int, code, msg string, details map[string]string) {
	if c.Writer.Written() {
		// A body is already on the wire (e.g. a panic mid-stream); writing a
		// second one would corrupt it. Stop the chain and let the log tell.
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(status, Body{
		Success:   false,
		Status:    status,
		Code:      code,
		Message:   msg,
		Details:   details,
		RequestID: requestid.From(c.Request.Context()),
	})
}
