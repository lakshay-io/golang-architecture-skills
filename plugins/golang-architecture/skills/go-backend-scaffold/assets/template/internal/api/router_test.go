package api_test

// These tests pin the HTTP architecture itself — middleware order, the error
// envelope, binding rules, probes, observability — using throwaway routes,
// so every domain module built on top inherits behaviour that is known to work.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/api"
	"example.com/goservice/internal/api/handler"
	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/config"
	"example.com/goservice/internal/platform/apperr"
	"example.com/goservice/internal/platform/health"
	"example.com/goservice/internal/platform/logger"
	"example.com/goservice/internal/platform/requestid"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// testRoutes stands in for a domain handler.
type testRoutes struct{}

func (testRoutes) Register(r gin.IRouter) {
	r.POST("/echo", handler.Wrap(func(c *gin.Context) error {
		var req struct {
			Name  string `json:"name" binding:"required,max=5"`
			Count int    `json:"count"`
		}
		if err := handler.BindJSON(c, &req); err != nil {
			return err
		}
		c.JSON(http.StatusOK, req)
		return nil
	}))
	r.GET("/search", handler.Wrap(func(c *gin.Context) error {
		var q struct {
			Limit int `form:"limit" binding:"omitempty,min=1,max=100"`
		}
		if err := handler.BindQuery(c, &q); err != nil {
			return err
		}
		c.JSON(http.StatusOK, q)
		return nil
	}))
	r.GET("/fail/:kind", handler.Wrap(func(c *gin.Context) error {
		switch c.Param("kind") {
		case "not_found":
			return apperr.NotFound("thing_not_found", "thing not found")
		case "conflict":
			return apperr.Conflict("thing_conflict", "thing changed")
		case "unavailable":
			return apperr.Unavailable("db_unavailable", "try again later")
		default:
			return errors.New("dial tcp 10.0.0.12:5432: connection refused")
		}
	}))
	r.GET("/panic", func(*gin.Context) { panic("boom") })
	r.GET("/slow", handler.Wrap(func(c *gin.Context) error {
		<-c.Request.Context().Done()
		return c.Request.Context().Err()
	}))
	r.GET("/txn", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"txn": newrelic.FromContext(c.Request.Context()) != nil})
	})
}

type fixture struct {
	h         http.Handler
	readiness *health.Readiness
	logs      *bytes.Buffer
}

func newFixture(t *testing.T, nr *newrelic.Application, checks ...health.Checker) fixture {
	t.Helper()
	var logs bytes.Buffer
	log := logger.New(&logs, config.Config{ServiceName: "svc", Env: config.EnvTest, Log: config.LogConfig{Format: "json"}}, nil)
	readiness := health.NewReadiness(time.Second, checks...)
	h, err := api.NewRouter(api.Options{
		Log:      log,
		HTTP:     config.HTTPConfig{MaxBodyBytes: 64, RequestTimeout: 50 * time.Millisecond},
		NewRelic: nr,
	}, api.Routes{
		Probes: []api.Registrar{handler.NewHealth(readiness, log)},
		V1:     []api.Registrar{testRoutes{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{h: h, readiness: readiness, logs: &logs}
}

func (f fixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f fixture) do(method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	return f.serve(req)
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) response.Payload {
	t.Helper()
	var env response.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not the error envelope: %v: %s", err, rec.Body.String())
	}
	return env.Error
}

func TestErrorEnvelope(t *testing.T) {
	f := newFixture(t, nil)
	tests := []struct {
		name, method, path, body string
		status                   int
		code                     string
		detail                   string // field expected in details, if any
	}{
		{"domain not found", "GET", "/v1/fail/not_found", "", 404, "thing_not_found", ""},
		{"domain conflict", "GET", "/v1/fail/conflict", "", 409, "thing_conflict", ""},
		{"dependency down", "GET", "/v1/fail/unavailable", "", 503, "db_unavailable", ""},
		{"unexpected error is masked", "GET", "/v1/fail/other", "", 500, "internal", ""},
		{"panic", "GET", "/v1/panic", "", 500, "internal", ""},
		{"request deadline", "GET", "/v1/slow", "", 504, "timeout", ""},
		{"unknown route", "GET", "/v1/nope", "", 404, "route_not_found", ""},
		{"wrong method", "DELETE", "/v1/echo", "", 405, "method_not_allowed", ""},
		{"missing field", "POST", "/v1/echo", `{"count":1}`, 400, "validation_failed", "name"},
		{"field too long", "POST", "/v1/echo", `{"name":"toolong"}`, 400, "validation_failed", "name"},
		{"unknown field", "POST", "/v1/echo", `{"name":"a","bogus":1}`, 400, "unknown_field", ""},
		{"duplicate key", "POST", "/v1/echo", `{"name":"a","name":"b"}`, 400, "duplicate_field", ""},
		{"key in a different case", "POST", "/v1/echo", `{"NAME":"a"}`, 400, "unknown_field", ""},
		{"data after the object", "POST", "/v1/echo", `{"name":"a"} {"count":1}`, 400, "invalid_json", ""},
		{"invalid UTF-8", "POST", "/v1/echo", "{\"name\":\"a\xffb\"}", 400, "invalid_json", ""},
		{"wrong type", "POST", "/v1/echo", `{"name":"a","count":"x"}`, 400, "invalid_json", ""},
		{"malformed JSON", "POST", "/v1/echo", `{"name":`, 400, "invalid_json", ""},
		{"empty body", "POST", "/v1/echo", "", 400, "invalid_json", ""},
		{"declared body too large", "POST", "/v1/echo", `{"name":"` + strings.Repeat("a", 100) + `"}`, 413, "body_too_large", ""},
		{"query out of range", "GET", "/v1/search?limit=500", "", 400, "validation_failed", "limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(tc.method, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status: got %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			p := decodeError(t, rec)
			if p.Code != tc.code {
				t.Errorf("code: got %q, want %q", p.Code, tc.code)
			}
			if p.RequestID == "" || p.RequestID != rec.Header().Get(requestid.Header) {
				t.Errorf("request id missing or not echoed: %q", p.RequestID)
			}
			if tc.detail != "" && p.Details[tc.detail] == "" {
				t.Errorf("want details[%q], got %v", tc.detail, p.Details)
			}
			if tc.status == 500 && (p.Message != "internal error" || strings.Contains(rec.Body.String(), "10.0.0.12")) {
				t.Errorf("internal details leaked to the client: %s", rec.Body.String())
			}
		})
	}
}

func TestChunkedBodyTooLarge(t *testing.T) {
	f := newFixture(t, nil)
	req := httptest.NewRequest("POST", "/v1/echo", strings.NewReader(`{"name":"`+strings.Repeat("a", 100)+`"}`))
	req.ContentLength = -1 // no declared length: the limit must hold while reading
	req.Header.Set("Content-Type", "application/json")
	rec := f.serve(req)
	if rec.Code != http.StatusRequestEntityTooLarge || decodeError(t, rec).Code != "body_too_large" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSuccessPassesThrough(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do("POST", "/v1/echo", `{"name":"ok","count":2}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"ok"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing")
	}
}

func TestRequestID(t *testing.T) {
	f := newFixture(t, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set(requestid.Header, "upstream-id-1")
	if got := f.serve(req).Header().Get(requestid.Header); got != "upstream-id-1" {
		t.Errorf("valid inbound id not kept: %q", got)
	}

	req = httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set(requestid.Header, "has spaces\nand newline")
	if got := f.serve(req).Header().Get(requestid.Header); got == "" || strings.ContainsAny(got, " \n") {
		t.Errorf("unsafe inbound id not replaced: %q", got)
	}
}

type failingCheck struct{}

func (failingCheck) Name() string                  { return "postgres" }
func (failingCheck) Check(_ context.Context) error { return errors.New("dial 10.0.0.12: timeout") }

func TestProbes(t *testing.T) {
	f := newFixture(t, nil)
	if rec := f.do("GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("liveness: got %d", rec.Code)
	}
	if rec := f.do("GET", "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness before start: got %d", rec.Code)
	}
	f.readiness.SetReady(true)
	if rec := f.do("GET", "/readyz", ""); rec.Code != http.StatusOK {
		t.Errorf("readiness after start: got %d", rec.Code)
	}

	degraded := newFixture(t, nil, failingCheck{})
	degraded.readiness.SetReady(true)
	rec := degraded.do("GET", "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("degraded readiness: got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "10.0.0.12") {
		t.Errorf("check error leaked publicly: %s", rec.Body.String())
	}
}

func TestAccessLog(t *testing.T) {
	f := newFixture(t, nil)
	f.do("GET", "/healthz", "")
	f.do("GET", "/v1/fail/other", "")

	lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one access-log line (probes skipped), got %d:\n%s", len(lines), f.logs.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["route"] != "/v1/fail/:kind" || rec["status"] != float64(500) || rec["level"] != "ERROR" {
		t.Errorf("unexpected access log: %v", rec)
	}
	if !strings.Contains(rec["err"].(string), "connection refused") || rec["request_id"] == nil {
		t.Errorf("access log must carry the cause and request id: %v", rec)
	}
}

func TestNewRelicTransactionInRequestContext(t *testing.T) {
	nr, err := newrelic.NewApplication(newrelic.ConfigEnabled(false), newrelic.ConfigAppName("svc-test"))
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, nr)
	rec := f.do("GET", "/v1/txn", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"txn":true`) {
		t.Fatalf("transaction not reachable via newrelic.FromContext: %d %s", rec.Code, rec.Body.String())
	}
}
