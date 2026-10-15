package response_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/platform/apperr"
	"example.com/goservice/internal/platform/requestid"
)

// render runs fn as a handler and returns the status and the body's keys.
func render(t *testing.T, fn gin.HandlerFunc) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req.WithContext(requestid.With(req.Context(), "req-1"))
	fn(c)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	return rec.Code, body
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The body shape is a contract shared by every service and every client:
// the same keys, whatever the status.
func TestBodyShape(t *testing.T) {
	t.Run("success: data, no details", func(t *testing.T) {
		status, body := render(t, func(c *gin.Context) {
			response.Success(c, http.StatusCreated, "thing_created", "Thing created", map[string]int{"n": 1})
		})
		if want := []string{"code", "data", "message", "requestId", "status", "success"}; !slices.Equal(keys(body), want) {
			t.Fatalf("keys %v, want %v", keys(body), want)
		}
		if status != 201 || body["success"] != true || body["status"] != 201.0 || body["code"] != "thing_created" || body["requestId"] != "req-1" {
			t.Fatalf("body %v", body)
		}
	})

	t.Run("success without a result still carries data {}", func(t *testing.T) {
		_, body := render(t, func(c *gin.Context) { response.Success(c, http.StatusOK, "ok", "OK", nil) })
		if d, ok := body["data"].(map[string]any); !ok || len(d) != 0 {
			t.Fatalf("data %v", body["data"])
		}
	})

	t.Run("failure with field details", func(t *testing.T) {
		status, body := render(t, func(c *gin.Context) {
			response.Error(c, apperr.Invalid("thing_invalid", "Thing is invalid").WithDetails(map[string]string{"name": "is required"}))
		})
		if want := []string{"code", "details", "message", "requestId", "status", "success"}; !slices.Equal(keys(body), want) {
			t.Fatalf("keys %v, want %v", keys(body), want)
		}
		if status != 400 || body["success"] != false || body["status"] != 400.0 || body["code"] != "thing_invalid" {
			t.Fatalf("body %v", body)
		}
	})

	t.Run("failure without details, and internals masked", func(t *testing.T) {
		status, body := render(t, func(c *gin.Context) { response.Error(c, errors.New("dial tcp 10.0.0.12:27017: refused")) })
		if want := []string{"code", "message", "requestId", "status", "success"}; !slices.Equal(keys(body), want) {
			t.Fatalf("keys %v, want %v", keys(body), want)
		}
		if status != 500 || body["status"] != 500.0 || body["code"] != "internal" || body["message"] != "internal error" {
			t.Fatalf("body %v", body)
		}
	})
}
