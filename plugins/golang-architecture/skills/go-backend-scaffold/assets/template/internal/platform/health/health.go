// Package health tracks readiness. It is transport-agnostic: the HTTP
// handler in internal/api/handler renders it as /healthz and /readyz.
//
//   - Liveness: "the process is not wedged". Never checks dependencies — a
//     database outage must not make the orchestrator restart every replica.
//   - Readiness: "send me traffic". False during startup, during shutdown
//     drain, and while a critical dependency check fails.
package health

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Checker is implemented by adapters that own a connection (DB pool, cache,
// broker) and register themselves in the composition root.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

type Readiness struct {
	ready   atomic.Bool
	checks  []Checker
	timeout time.Duration
}

func NewReadiness(timeout time.Duration, checks ...Checker) *Readiness {
	return &Readiness{checks: checks, timeout: timeout}
}

func (r *Readiness) SetReady(v bool) { r.ready.Store(v) }

// Status reports whether the service should receive traffic. failed maps a
// check name to its error; it is empty when the service is simply not
// started yet or draining.
func (r *Readiness) Status(ctx context.Context) (ready bool, failed map[string]string) {
	if !r.ready.Load() {
		return false, nil
	}
	failed = r.runChecks(ctx)
	return len(failed) == 0, failed
}

// runChecks runs every check concurrently under one deadline, so readiness
// latency is the slowest check, not the sum of all of them.
func (r *Readiness) runChecks(ctx context.Context) map[string]string {
	if len(r.checks) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed = map[string]string{}
	)
	for _, c := range r.checks {
		wg.Go(func() {
			if err := c.Check(ctx); err != nil {
				mu.Lock()
				failed[c.Name()] = err.Error()
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return failed
}
