// Package app is the composition root: the ONE place that knows every
// concrete type. It builds adapters, injects them into domain services,
// hands services to transport handlers, and owns the process lifecycle.
//
// Wiring is plain constructor injection — no DI container, no init()
// side effects, no package-level singletons — so the dependency graph is
// readable top to bottom and every piece is testable in isolation.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api"
	"example.com/goservice/internal/api/handler"
	"example.com/goservice/internal/config"
	"example.com/goservice/internal/platform/health"
	"example.com/goservice/internal/platform/logger"
	"example.com/goservice/internal/platform/telemetry"
)

type App struct {
	cfg       config.Config
	log       *slog.Logger
	readiness *health.Readiness
	server    *http.Server
	closers   []closer

	started chan struct{}
	addr    net.Addr
}

// closer releases one adapter (pool, client, agent) during shutdown.
type closer struct {
	name string
	fn   func(context.Context) error
}

func New(cfg config.Config) (*App, error) {
	a := &App{cfg: cfg, started: make(chan struct{})}

	// ---- Observability first: everything after this can log and be traced.
	bootLog := logger.New(os.Stdout, cfg, nil)
	nr, err := telemetry.NewRelic(cfg, bootLog)
	if err != nil {
		return nil, fmt.Errorf("start new relic: %w", err)
	}
	// Registered first so it closes LAST: the final harvest then includes
	// whatever the other adapters logged while shutting down.
	a.onShutdown("newrelic", func(ctx context.Context) error {
		telemetry.Shutdown(ctx, nr)
		return nil
	})
	a.log = logger.New(os.Stdout, cfg, telemetry.LogHandler(nr))
	slog.SetDefault(a.log)

	// ---- Adapters: databases, caches, brokers, outbound clients. Each one
	// that holds connections registers a readiness check and a closer:
	//
	//	db, err := postgres.Open(ctx, cfg.Postgres)  // pooled, instrumented
	//	checks = append(checks, db)                  // implements health.Checker
	//	a.onShutdown("postgres", db.Close)
	var checks []health.Checker

	// ---- Domain services: one per module, built from adapters.
	//
	//	orders := order.NewService(postgres.NewOrderRepo(db), idgen.NewV7, time.Now)

	// ---- Transport.
	a.readiness = health.NewReadiness(2*time.Second, checks...)
	gin.SetMode(gin.ReleaseMode) // our own structured logs replace Gin's debug output
	engine, err := api.NewRouter(
		api.Options{Log: a.log, HTTP: cfg.HTTP, NewRelic: nr},
		api.Routes{
			Probes: []api.Registrar{handler.NewHealth(a.readiness, a.log)},
			V1:     []api.Registrar{
				// handler.NewOrderHandler(orders),
			},
		},
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("build router: %w", err), a.close(context.Background()))
	}
	for _, rt := range engine.Routes() {
		a.log.Debug("route registered", slog.String("method", rt.Method), slog.String("path", rt.Path))
	}
	a.server = api.NewServer(cfg.HTTP, engine, a.log)
	return a, nil
}

func (a *App) onShutdown(name string, fn func(context.Context) error) {
	a.closers = append(a.closers, closer{name: name, fn: fn})
}

// Started is closed once the server is listening; Addr is valid after that.
func (a *App) Started() <-chan struct{} { return a.started }
func (a *App) Addr() net.Addr           { return a.addr }

// Run serves until ctx is cancelled (SIGTERM/SIGINT) or the server fails,
// then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	// Bind before reporting ready: a taken port fails fast, at boot.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.server.Addr)
	if err != nil {
		return errors.Join(fmt.Errorf("listen on %s: %w", a.server.Addr, err), a.shutdown(ctx, false))
	}
	a.addr = ln.Addr()
	close(a.started)

	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(ln) }()

	a.readiness.SetReady(true)
	a.log.Info("server started", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return errors.Join(fmt.Errorf("http server: %w", err), a.shutdown(ctx, false))
	case <-ctx.Done():
		a.log.Info("shutdown signal received")
		return a.shutdown(ctx, true)
	}
}

// shutdown runs the graceful sequence, bounded by Shutdown.Timeout:
//  1. report not-ready, then keep serving for DrainDelay so the load
//     balancer stops sending new requests (no 502s during deploys);
//  2. stop accepting connections and wait for in-flight requests;
//  3. close adapters in reverse creation order (dependents first).
func (a *App) shutdown(ctx context.Context, drain bool) error {
	a.readiness.SetReady(false)
	// The run context is already cancelled by the time we get here.
	// WithoutCancel keeps its values (logger, trace) but gives the shutdown
	// sequence its own deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.Shutdown.Timeout)
	defer cancel()

	if drain {
		t := time.NewTimer(a.cfg.Shutdown.DrainDelay)
		select {
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}

	var errs []error
	if err := a.server.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if err := a.close(ctx); err != nil {
		errs = append(errs, err)
	}
	a.log.Info("shutdown complete")
	return errors.Join(errs...)
}

func (a *App) close(ctx context.Context) error {
	var errs []error
	for i := len(a.closers) - 1; i >= 0; i-- {
		c := a.closers[i]
		if err := c.fn(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", c.name, err))
		}
	}
	return errors.Join(errs...)
}
