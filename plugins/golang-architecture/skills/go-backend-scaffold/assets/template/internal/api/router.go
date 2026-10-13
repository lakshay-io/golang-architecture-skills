// Package api is the Gin HTTP transport: engine setup, the global middleware
// chain, route mounting and the http.Server. It translates between HTTP and
// the domain and holds no business rules.
package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/api/handler"
	"example.com/goservice/internal/api/middleware"
	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/config"
)

// Registrar is implemented by every handler: it mounts its own routes on the
// group it is given. The router never needs to know which domains exist.
type Registrar interface {
	Register(r gin.IRouter)
}

// Routes lists handlers by API surface. A new domain module adds one entry
// to V1; a breaking API change adds a V2 field mounted at /v2 next to /v1.
type Routes struct {
	Probes []Registrar // mounted at "/": unversioned, not logged, not monitored
	V1     []Registrar // mounted at "/v1"
}

type Options struct {
	Log      *slog.Logger
	HTTP     config.HTTPConfig
	NewRelic *newrelic.Application // nil disables instrumentation (tests)
}

// NewRouter builds the Gin engine. Call gin.SetMode once in the composition
// root before this (it is process-global).
func NewRouter(o Options, routes Routes) (*gin.Engine, error) {
	handler.ConfigureBinding()

	r := gin.New() // not gin.Default(): its logger and recovery are unstructured
	// Make c.Done()/c.Err()/c.Value() follow c.Request.Context(), so code that
	// passes c where a context is expected still sees the deadline.
	r.ContextWithFallback = true
	r.HandleMethodNotAllowed = true
	// Gin trusts EVERY proxy by default, letting any client spoof
	// c.ClientIP() with X-Forwarded-For. Trust only configured load balancers.
	if err := r.SetTrustedProxies(o.HTTP.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}

	// Order is load-bearing (outermost first):
	//   RequestID  — before everything, so every log line and trace has it
	//   NewRelic   — before AccessLog: logs recorded after the transaction
	//                ends are dropped by the agent
	//   AccessLog  — outside Recovery, so a panic is still logged as a 500
	//   Recovery   — turns panics into 500s
	//   SecureHeaders, BodyLimit, Timeout — per-request guards
	r.Use(middleware.RequestID())
	r.Use(middleware.NewRelic(o.NewRelic, handler.ProbeRoutes...)...)
	r.Use(
		middleware.AccessLog(o.Log, handler.ProbeRoutes...),
		middleware.Recovery(o.Log),
		middleware.SecureHeaders(),
		middleware.BodyLimit(o.HTTP.MaxBodyBytes),
		middleware.Timeout(o.HTTP.RequestTimeout),
	)
	r.NoRoute(func(c *gin.Context) {
		response.Error(c, &response.HTTPError{Status: http.StatusNotFound, Code: "route_not_found", Message: "no route matches this path"})
	})
	r.NoMethod(func(c *gin.Context) {
		response.Error(c, &response.HTTPError{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "method not allowed on this path"})
	})

	for _, reg := range routes.Probes {
		reg.Register(r)
	}
	v1 := r.Group("/v1")
	for _, reg := range routes.V1 {
		reg.Register(v1)
	}
	return r, nil
}

// NewServer wraps the engine in an http.Server with every timeout set.
// Never use engine.Run(): it builds a server with NO timeouts (slow or idle
// clients can pin goroutines and sockets forever) and offers no handle for
// graceful shutdown.
func NewServer(cfg config.HTTPConfig, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
