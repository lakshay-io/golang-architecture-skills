package handler

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/platform/apperr"
)

// Validation runs in two layers, both independent of any database:
//   - this file checks the WIRE format: strict JSON decoding, then the
//     binding tags on request DTOs (presence, lengths, ranges);
//   - the domain checks BUSINESS invariants, whichever transport (HTTP,
//     gRPC, a queue consumer) the call came from.
// Storage adapters never validate requests, so swapping the database
// changes nothing here.

var configureOnce sync.Once

// ConfigureBinding applies process-wide Gin binding settings exactly once:
// validation errors are reported by their wire name ("nextCursor"), not the
// Go field name ("NextCursor"), and Gin's own JSON binding (if anyone calls
// c.ShouldBindJSON directly) still rejects unknown fields.
func ConfigureBinding() {
	configureOnce.Do(func() {
		binding.EnableDecoderDisallowUnknownFields = true
		if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
			v.RegisterTagNameFunc(wireName)
		}
	})
}

func wireName(f reflect.StructField) string {
	for _, key := range [...]string{"json", "form", "uri", "header"} {
		name, _, _ := strings.Cut(f.Tag.Get(key), ",")
		if name != "" && name != "-" {
			return name
		}
	}
	return f.Name
}

// BindJSON decodes the request body into dst with encoding/json/v2, then runs
// dst's binding tags.
//
// encoding/json (v1, which Gin uses) accepts input that attackers rely on to
// make two parsers disagree. v2's defaults reject all of it:
//   - duplicate keys ({"admin":false,"admin":true}: v1 silently keeps the last);
//   - keys in a different case ({"ADMIN":true}: v1 matches it to "admin");
//   - invalid UTF-8 (v1 silently replaces it with U+FFFD);
//   - data after the object ({"a":1} {"b":2}: v1 ignores the rest);
//
// and RejectUnknownMembers turns unknown fields into errors too.
func BindJSON(c *gin.Context, dst any) error {
	if c.Request.Body == nil {
		return apperr.Invalid("invalid_json", "request body must not be empty")
	}
	body, err := io.ReadAll(c.Request.Body) // bounded by middleware.BodyLimit
	if err != nil {
		return bindError(err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return apperr.Invalid("invalid_json", "request body must not be empty")
	}
	if err := json.Unmarshal(body, dst, json.RejectUnknownMembers(true)); err != nil {
		return bindError(err)
	}
	if err := binding.Validator.ValidateStruct(dst); err != nil {
		return bindError(err)
	}
	return nil
}

// BindQuery decodes the query string into dst and runs its binding tags.
func BindQuery(c *gin.Context, dst any) error {
	if err := c.ShouldBindQuery(dst); err != nil {
		return bindError(err)
	}
	return nil
}

// bindError translates decoder and validator errors into the API's error
// vocabulary, so a malformed request gets a precise, stable error code.
func bindError(err error) error {
	if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &response.HTTPError{
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "body_too_large",
			Message: fmt.Sprintf("request body must not exceed %d bytes", tooLarge.Limit),
		}
	}
	if verrs, ok := errors.AsType[validator.ValidationErrors](err); ok {
		details := make(map[string]string, len(verrs))
		for _, fe := range verrs {
			details[fe.Field()] = describe(fe)
		}
		return apperr.Invalid("validation_failed", "request is invalid").WithDetails(details)
	}
	if errors.Is(err, json.ErrUnknownName) {
		return apperr.Invalid("unknown_field", "unknown field "+pointerName(err))
	}
	if errors.Is(err, jsontext.ErrDuplicateName) {
		return apperr.Invalid("duplicate_field", "field "+pointerName(err)+" appears more than once")
	}
	if serr, ok := errors.AsType[*json.SemanticError](err); ok {
		return apperr.Invalid("invalid_json", fmt.Sprintf("field %q has the wrong type", strings.TrimPrefix(string(serr.JSONPointer), "/")))
	}
	if _, ok := errors.AsType[*jsontext.SyntacticError](err); ok || errors.Is(err, io.ErrUnexpectedEOF) {
		return apperr.Invalid("invalid_json", "request body is not valid JSON")
	}
	return apperr.Invalid("invalid_request", err.Error())
}

// pointerName returns the quoted JSON field an error points at, e.g. "admin".
func pointerName(err error) string {
	var ptr jsontext.Pointer
	if serr, ok := errors.AsType[*json.SemanticError](err); ok {
		ptr = serr.JSONPointer
	} else if serr, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		ptr = serr.JSONPointer
	}
	return fmt.Sprintf("%q", strings.TrimPrefix(string(ptr), "/"))
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min", "gte":
		return "must be at least " + fe.Param()
	case "max", "lte":
		return "must be at most " + fe.Param()
	case "gt":
		return "must be greater than " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	default:
		return "failed the " + fe.Tag() + " rule"
	}
}
