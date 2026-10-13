# Architecture

Every Go backend service follows this structure, so an engineer (or Claude) who knows one service knows them all. Each rule below says *why*: when a rule and a real need conflict, the reason tells you whether the rule still applies.

**Goals:**
- **Horizontal scale.** Replicas are stateless and interchangeable.
- **Operability.** Every request is observable, and deploys don't drop traffic.
- **Changeability.** Business logic is isolated from frameworks and infrastructure.

## Layout

```
cmd/server/            process entry: signals, config, exit code. Nothing else.
internal/
  app/                 composition root: builds every concrete type, owns the lifecycle
  config/              typed env config, validated once at boot
  api/                 HTTP transport (Gin)
    router.go          engine, middleware order, route mounting, http.Server
    middleware/        request id, New Relic, access log, recovery, limits, timeout
    response/          error envelope + domain-error → HTTP-status mapping
    handler/           one file per module: bind → call service → render
  domain/<module>/     entities, invariants, use cases (Service), ports (interfaces)
  storage/<driver>/    adapters implementing domain ports (postgres, mongo, redis…)
  platform/            framework-agnostic infrastructure helpers
    apperr/            the shared error vocabulary
    logger/            slog setup (request id + New Relic logs in context)
    telemetry/         New Relic agent setup
    httpclient/        outbound HTTP client (timeouts, pooling, tracing)
    health/            readiness state and dependency checks
    requestid/         correlation id in context
docs/                  this file, ADDING_A_MODULE.md, OBSERVABILITY.md
```

## The dependency rule

```
cmd/server ──► app ──► api ─────► domain ◄───── storage
                │                   ▲              ▲
                └───────────────────┴──────────────┘   (app wires them together)
platform/*  ◄── used by any layer; imports none of them
```

1. **`domain` imports only the standard library and `platform/apperr`.** It never imports Gin, New Relic or a database driver. Business rules can then be tested in milliseconds without infrastructure, and replacing a framework or database never touches them.
2. **Interfaces are declared by the consumer, and kept small.**
   - The domain declares the `Repository` it needs, and storage implements it.
   - A handler declares the slice of the service it calls.
   - The result is no import cycles, easy test stubs, and no interface-per-struct boilerplate.
3. **Only `internal/app` knows concrete types.** Wiring is plain constructor injection. There are no DI frameworks, no `init()` side effects and no package-level singletons, so the whole dependency graph reads top to bottom in one file.
4. **Wire types are not domain types.**
   - Handlers have their own request and response structs, so a refactor can never silently change the public API.
   - Storage adapters map rows and documents to entities, so the schema can change without touching the domain.
5. **`platform` holds no business logic and imports no other layer.** That way it can be copied between services unchanged.

## Request lifecycle

```
RequestID → NewRelic txn → AccessLog → Recovery → SecureHeaders → BodyLimit → Timeout → handler
```

The order is load-bearing, and `internal/api/router.go` explains why at each step.

Inside a handler:
1. Bind and validate the wire format (`handler.BindJSON` / `BindQuery`).
2. Call the service with `c.Request.Context()`.
3. Render the result, or `return err`.

`handler.Wrap` sends every error to `response.Error`, which:
- maps `apperr.Kind` to an HTTP status;
- hides internal details from the client (5xx always says "internal error");
- attaches the full cause, which the access log and New Relic report.

## Errors

- **Domain errors.** They are `apperr` sentinels with a stable `Code` that clients can switch on, e.g. `apperr.NotFound("order_not_found", "order not found")`.
- **Add context as an error passes upward:** `fmt.Errorf("load order %s: %w", id, err)`. Sentinels still match with `errors.Is`.
- **Log once, at the edge.** Inner layers return errors and the access log records them once. Logging at every layer creates duplicate, context-free noise.
- **Every error response goes through `response.Error`.** Never write an error body by hand: New Relic's error reporting and the access log both depend on it.

## Context, cancellation and concurrency

- **Every I/O function takes `ctx context.Context` as its first parameter**, passed down from `c.Request.Context()`. The per-request deadline (`HTTP_REQUEST_TIMEOUT`) then cancels database queries and outbound calls, instead of leaving them running for a client that has already gone.
- **Never store a context in a struct.** Never let `*gin.Context` escape a handler: Gin pools and reuses it as soon as the handler returns.
- **Every goroutine has an owner, a stop signal (ctx) and someone who waits for it at shutdown.**
  - No fire-and-forget goroutines in handlers. Hand background work to a bounded worker owned by `app`, and register it with `onShutdown`.
  - Bound fan-out with `errgroup.SetLimit` or a semaphore. Unbounded concurrency turns a traffic spike into an outage.
- **Shared state:** prefer ownership and immutability. Use a mutex for small critical sections and atomics for flags and counters.
- **`make test` always runs with `-race`.**

## Scalability rules

- **Stateless replicas.**
  - Anything that must be shared across replicas lives in Postgres, Redis or another shared store: sessions, rate-limit counters, caches that must stay coherent.
  - In-process caches are allowed only for data that may be briefly stale.
- **Timeouts on everything.**
  - Inbound: the server timeouts plus a per-request deadline.
  - Outbound: always use `platform/httpclient`, never `http.DefaultClient`.
  - Database: driven by ctx.
- **Retries:**
  - Retry only idempotent operations, with exponential backoff and jitter.
  - Make POSTs with side effects idempotent (idempotency key, unique constraint, upsert), because load balancers, clients and queues all retry.
- **Backpressure.** Bound every queue and pool. When saturated, answer 429 or 503 quickly rather than queueing without limit.
- **Pagination** uses an opaque keyset cursor (`?limit=&cursor=` → `next_cursor`), with page size capped. Never use `OFFSET` on large tables.
- **IDs** are UUIDv7, generated in the service:
  - they are time-ordered, so database index inserts stay local;
  - replicas don't need to coordinate to generate them.
- **Concurrent writes** use optimistic concurrency (a `version` column with compare-and-swap) instead of holding row locks across a request.
- **"Write to the database and publish an event"** uses the transactional outbox pattern, never two independent writes.
- **Connection pools** are sized per replica so that replicas × pool size stays below the database's connection limit.
- **Caching** is cache-aside with TTL plus jitter, and `singleflight` to stop stampedes. Measure first.
- **Zero-downtime deploys.**
  1. On SIGTERM, readiness flips to not-ready.
  2. The service keeps serving for `SHUTDOWN_DRAIN_DELAY`.
  3. It finishes in-flight requests.
  4. It closes adapters in reverse order.
  5. Set the platform's kill grace period above `SHUTDOWN_TIMEOUT`.
- **Runtime:**
  - Go ≥ 1.25 sets `GOMAXPROCS` from the container CPU limit.
  - Set `GOMEMLIMIT` to about 90% of the container memory limit, so the GC works harder before the kernel kills the process.

## Performance rules (measure first)

- **Profile before optimizing.** Use New Relic for production hotspots and `go test -bench` (`b.Loop()`) or `pprof` locally.
- **Reuse expensive objects.** Create HTTP clients, DB pools and compiled regexps once at startup, never per request.
- **Database:**
  - One round trip per use case where possible; no N+1 queries.
  - Select only the columns you need.
  - Every query's filter and sort is backed by an index.
- **Allocations in hot paths:**
  - Preallocate slices and maps when the size is known.
  - Use `strings.Builder` for building strings.
  - Use `sync.Pool` only when a profile proves it helps.

## Configuration

- **Environment variables only.** Read once in `internal/config` and passed down typed. Nothing else calls `os.Getenv`.
- **Validated at boot.** Every problem is reported at once, so a bad deploy fails before taking traffic.
- **Secrets are never logged.** Types holding them implement `slog.LogValuer` to redact.

## Logging

- **Format:** structured `slog`, JSON in deployed environments.
- **Inside a request:** use the `*Context` methods (`InfoContext(ctx, ...)`). They add `request_id` and link the line to its New Relic trace.
- **One access-log line per request**, carrying the route template, status, duration and error cause.
- **Levels:**
  - `Error`: a human needs to look at this.
  - `Warn`: degraded, but handled.
  - `Info`: lifecycle events and requests.
  - `Debug`: development detail.
- **Never log** secrets, tokens or personal data.

## API conventions

- **Paths:** routes are versioned under `/v1`. A breaking change ships as `/v2` next to `/v1`. Resource names are plural nouns.
- **JSON:** fields are `snake_case`; timestamps are RFC 3339 in UTC.
- **Status codes:**
  - Create: `201` with a `Location` header.
  - Delete: `204`.
  - Partial update: `PATCH` carrying the `version` the client last read; a stale version gets `409`.
- **Errors** always use the envelope: `{"error":{"code","message","details","request_id"}}`.
- **Validation is split by responsibility:**
  - Binding tags check the wire shape: presence, types, ranges.
  - The domain checks business invariants, whatever transport the call came from.

## Testing strategy

| Layer | How | Speed |
|---|---|---|
| domain | table-driven unit tests with in-memory fakes of the ports | ms |
| storage | integration tests against a real database (testcontainers), behind the `integration` build tag | s |
| api | `httptest` through the real router and middleware (see `internal/api/router_test.go`) | ms |
| app | boots the process, probes it and shuts it down (see `internal/app/app_test.go`) | s |

`make verify` (gofmt, vet, module integrity, race tests, build) must pass before merge.
