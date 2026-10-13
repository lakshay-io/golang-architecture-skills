// Package response renders every API response that is not a plain success
// body: the error envelope, and the mapping from domain error kinds to HTTP
// status codes. Handlers and middleware both go through it, so every error the
// API returns has the same shape:
//
//	{"error": {"code": "item_not_found", "message": "...", "details": {...}, "request_id": "..."}}
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

type Envelope struct {
	Error Payload `json:"error"`
}

type Payload struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// HTTPError is a transport-level failure with an explicit status: things the
// domain never says, such as 404 for an unknown route, 405, 413 or 429.
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

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

// Error renders err as the standard envelope and aborts the chain.
//
// The full error (with its wrapped cause chain) is attached to the gin context
// so the access log records it; clients only ever see the safe Message.
func Error(c *gin.Context, err error) {
	_ = c.Error(err)

	if he, ok := errors.AsType[*HTTPError](err); ok {
		abort(c, he.Status, Payload{Code: he.Code, Message: he.Message})
		return
	}
	// Deadlines and disconnects are not application bugs: give them their own
	// statuses so they never inflate the 5xx error-rate alert.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		abort(c, http.StatusGatewayTimeout, Payload{Code: "timeout", Message: "request timed out"})
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
	abort(c, StatusFor(e.Kind), Payload{Code: e.Code, Message: msg, Details: e.Details})
}

func abort(c *gin.Context, status int, p Payload) {
	if c.Writer.Written() {
		// A body is already on the wire (e.g. a panic mid-stream); writing a
		// second one would corrupt it. Stop the chain and let the log tell.
		c.Abort()
		return
	}
	p.RequestID = requestid.From(c.Request.Context())
	c.AbortWithStatusJSON(status, Envelope{Error: p})
}
