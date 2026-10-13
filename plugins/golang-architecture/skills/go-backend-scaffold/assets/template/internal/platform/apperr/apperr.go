// Package apperr is the one error vocabulary shared by every layer.
//
// Domain code says WHAT went wrong (Kind + stable Code); each transport decides
// how to express it (HTTP status, gRPC code, retry/no-retry for a consumer).
// That keeps net/http out of the domain and keeps status mapping in one place.
package apperr

import (
	"errors"
	"fmt"
)

type Kind uint8

const (
	KindInternal     Kind = iota // bug or unexpected failure; never show details to clients
	KindInvalid                  // caller sent bad input
	KindNotFound                 // resource does not exist (or caller may not know it does)
	KindConflict                 // state conflict: duplicate, stale version
	KindUnauthorized             // no or bad credentials
	KindForbidden                // authenticated but not allowed
	KindUnavailable              // dependency down or overloaded; safe to retry
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// Error carries a Kind, a stable machine-readable Code clients can switch on,
// a human Message that is safe to return, optional field-level Details, and
// the wrapped cause (logged, never returned to clients).
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is match sentinel *Error values by Kind+Code, so a sentinel
// stays matchable after it is wrapped with extra context.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind && t.Code == e.Code
}

func New(kind Kind, code, msg string) *Error { return &Error{Kind: kind, Code: code, Message: msg} }

func Invalid(code, msg string) *Error     { return New(KindInvalid, code, msg) }
func NotFound(code, msg string) *Error    { return New(KindNotFound, code, msg) }
func Conflict(code, msg string) *Error    { return New(KindConflict, code, msg) }
func Unavailable(code, msg string) *Error { return New(KindUnavailable, code, msg) }

// Wrap attaches a cause to a copy of e, leaving the sentinel untouched.
func (e *Error) Wrap(cause error) *Error {
	c := *e
	c.Err = cause
	return &c
}

// WithDetails returns a copy of e carrying field-level validation details.
func (e *Error) WithDetails(d map[string]string) *Error {
	c := *e
	c.Details = d
	return &c
}

// As returns the *Error in err's chain, or an internal error wrapping err.
func As(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return &Error{Kind: KindInternal, Code: "internal", Message: "internal error", Err: err}
}

// KindOf reports the Kind of err (KindInternal for foreign errors).
func KindOf(err error) Kind { return As(err).Kind }
