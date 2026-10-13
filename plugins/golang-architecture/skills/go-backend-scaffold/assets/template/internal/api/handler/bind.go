package handler

import (
	"encoding/json"
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

// Validation is split by responsibility:
//   - binding tags on request DTOs check the WIRE shape (presence, types,
//     ranges of query params) and fail before any domain code runs;
//   - the domain validates BUSINESS invariants, whichever transport (HTTP,
//     gRPC, a queue consumer) the call came from.

var configureOnce sync.Once

// ConfigureBinding applies process-wide Gin binding settings exactly once:
// reject unknown JSON fields (catches client typos and contract drift), and
// report validation errors by their wire name ("next_cursor"), not the Go
// field name ("NextCursor").
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

// BindJSON decodes the JSON body into dst and runs its binding tags.
func BindJSON(c *gin.Context, dst any) error {
	if err := c.ShouldBindJSON(dst); err != nil {
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
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return apperr.Invalid("invalid_json", fmt.Sprintf("field %q has the wrong type", typeErr.Field))
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); ok || errors.Is(err, io.ErrUnexpectedEOF) {
		return apperr.Invalid("invalid_json", "request body is not valid JSON")
	}
	if errors.Is(err, io.EOF) {
		return apperr.Invalid("invalid_json", "request body must not be empty")
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return apperr.Invalid("unknown_field", "unknown field "+field)
	}
	return apperr.Invalid("invalid_request", err.Error())
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
