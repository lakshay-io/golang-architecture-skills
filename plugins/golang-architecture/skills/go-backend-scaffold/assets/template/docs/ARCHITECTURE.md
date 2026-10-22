# Architecture

Every Go backend service follows this structure, so an engineer (or Claude) who knows one service knows them all. Each rule below says *why*: when a rule and a real need conflict, the reason tells you whether the rule still applies.

**Goals:**
- **Horizontal scale.** Replicas are stateless and interchangeable.
- **Operability.** Every request is observable, and deploys don't drop traffic.
- **Changeability.** Business logic is isolated from frameworks and infrastructure.

## Layout

```
cmd/server/            process entry: signals, config, exit code. Nothing else.
cmd/openapi/           writes docs/openapi.json from the handlers' Docs (make openapi)
cmd/<tool>/            other binaries (migrate, dev and ops tools): same shape, each calls one app.<Root>
internal/
  app/                 composition root: builds every concrete type, owns the lifecycle
                       (routes.go lists the handlers; openapi.go describes the API)
  config/              typed env config, validated once at boot
  api/                 HTTP transport (Gin)
    router.go          engine, middleware order, route mounting, http.Server
    middleware/        request id, New Relic, access log, recovery, limits, timeout
    response/          the one response body (Success, Error) + domain-error → HTTP-status mapping
    handler/           one file per module: bind → call service → render, plus Docs
    apidoc/            OpenAPI 3.1 from the handlers' Docs and DTO types
    openapi.go         Spec: checks Docs against the mounted routes, renders the document
  <transport>/         another way calls arrive (e.g. trigger/ for stream and queue events): decode → service → errors
  domain/<module>/     entities, invariants, use cases (Service), ports (interfaces)
  storage/<driver>/    adapters implementing domain ports (postgres, mongo, redis…)
  platform/            framework-agnostic infrastructure helpers
    apperr/            the shared error vocabulary
    logger/            slog setup (request id + New Relic logs in context)
    telemetry/         New Relic agent setup
    httpclient/        outbound HTTP client (timeouts, pooling, tracing)
    health/            readiness state and dependency checks
    requestid/         correlation id in context
testdata/contract/     golden files of a contract shared with another service (optional)
docs/                  this file, ADDING_A_MODULE.md, OBSERVABILITY.md, SECURITY.md,
                       openapi.json (generated: make openapi)
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
6. **Modules do not import each other.** When one module must react to another (a rule saved in one is published by another), the module that acts declares a port and `app` wires an adapter between them, so each module can be tested, changed and removed alone. One exception: a module that defines a shared contract for plug-ins (a record type and a `Handler` interface every module of that kind implements) may be imported by those plug-ins, in that one direction only, never back.

## Request lifecycle

```
RequestID → NewRelic txn → AccessLog → Recovery → SecureHeaders → BodyLimit → Timeout → handler
```

The order is load-bearing, and `internal/api/router.go` explains why at each step.

Inside a handler:
1. Bind and validate the wire format (`handler.BindJSON` / `BindQuery`).
2. Call the service with `c.Request.Context()`.
3. Render the result with `response.Success`, or `return err`.

`handler.Wrap` sends every error to `response.Error`, which:
- maps `apperr.Kind` to an HTTP status;
- hides internal details from the client (5xx always says "internal error");
- attaches the full cause, which the access log and New Relic report.

**The same binary runs as a process or in AWS Lambda.** When the Lambda runtime sets `AWS_LAMBDA_FUNCTION_NAME`, `config.Lambda` is true and `app.Run` serves API Gateway HTTP API events (payload 2.0) through the same router, middleware and handlers, via the Gin adapter (`app.LambdaHandler`). Everything `app.New` builds (pools, clients) is created once per execution environment and reused by every invocation. Lambda freezes the process between invocations and ends it without a drain, so there is no graceful shutdown there; locally and in a container nothing changes.

**Other transports** live beside `api/`, one package per kind of trigger, for example `internal/trigger` for Kinesis and SQS events. They follow the handler rules: decode the event into the transport's own types, call the service with the event's `ctx`, and decide per error whether the event is retried (an `apperr.KindUnavailable` or unknown error) or dropped and reported (a record that can never be processed), returning partial batch failures so one bad record never blocks the rest.

## Errors

- **Domain errors.** They are `apperr` sentinels with a stable `Code` that clients can switch on, e.g. `apperr.NotFound("order_not_found", "order not found")`.
- **Add context as an error passes upward:** `fmt.Errorf("load order %s: %w", id, err)`. Sentinels still match with `errors.Is`.
- **Log once, at the edge.** Inner layers return errors and the access log records them once. Logging at every layer creates duplicate, context-free noise.
- **Every error response goes through `response.Error`.** Never write an error body by hand: New Relic's error reporting and the access log both depend on it. Every success goes through `response.Success`, so both share one body shape (see API conventions).

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
- **Pagination** uses an opaque keyset cursor (`?limit=&cursor=` → `nextCursor`), with page size capped. Never use `OFFSET` on large tables.
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

## Schema and migrations

- **A store this service owns gets its schema from a command, not from the first write:** `cmd/migrate` (`make migrate [ENV_FILE=…]`) creates every collection, table and index the adapters rely on, checks the ones that exist, and never drops or changes data, so running it twice changes nothing.
- **At startup the service checks the schema it relies on** (a unique index that keeps writes correct, a time-series collection that cannot be converted later) and refuses to start, naming `make migrate`, when it is missing. A deploy then fails before taking traffic instead of writing duplicates.
- **Order:** run the migration, then deploy. Collections another system owns get their indexes from that system.

## Configuration

- **Environment variables only.** Read once in `internal/config` and passed down typed. Nothing else calls `os.Getenv`.
- **Validated at boot.** Every problem is reported at once, so a bad deploy fails before taking traffic.
- **Secrets are never logged.** Types holding them implement `slog.LogValuer` to redact.

## Security

[SECURITY.md](SECURITY.md) maps the OWASP API Security Top 10 and Top 10 to this base: what the base already does (strict JSON decoding, body and time limits, security headers, error hiding, redacted secrets) and what every module must do (object-level authorization in every query, bounded lists, no URLs from requests). Its checklist is part of every module's review, and CI runs `make verify lint vuln`.

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
- **JSON:** fields are `camelCase`; timestamps are RFC 3339 in UTC.
- **Status codes:**
  - Create: `201` with a `Location` header.
  - Delete: `204`.
  - Partial update: `PATCH` carrying the `version` the client last read; a stale version gets `409`.
- **Every response, success or failure, has one body shape**, the same in every service built on this base:
  - `{"success","status","code","message","data","requestId"}` on success;
  - `{"success","status","code","message","details","requestId"}` on failure.
  - `success`, `status`, `code`, `message` and `requestId` are always there. `data` is on every success and never on a failure; `details` only on a failure about specific fields.
  - Handlers render a success with `response.Success(c, status, code, message, data)` and `return err` for a failure. Nothing calls `c.JSON` directly.
- **Validation is split by responsibility, and none of it lives in storage**, so switching databases never touches it:
  - JSON bodies are decoded with `encoding/json/v2` (`handler.BindJSON`). It rejects duplicate keys, keys in a different case, unknown fields, invalid UTF-8 and data after the object. Go's older `encoding/json` accepts all of them, and attackers use that to make a proxy and the service read the same request differently.
  - Binding tags check the wire shape: presence, types, ranges.
  - The domain checks business invariants, whatever transport the call came from.

## API documentation

Every service publishes `docs/openapi.json`, an OpenAPI 3.1 document generated from the code, so the frontend and mobile teams read the API from the branch they build against, not from a hand-written page that drifts.

- **Docs live next to the routes.** Every handler has a `Docs() apidoc.Group` method beside `Register`. Each op names the route, its auth, its headers and parameters, its body, every success and every failure: status, code, message and when it happens.
- **Schemas come from the code.** `internal/api/apidoc` reflects the request and response DTOs: `json` tags give the names, `binding` tags the rules, and an optional `doc:"…"` tag a description. A field the domain requires (rather than a binding tag) is marked `api:"required"`. A type with its own JSON methods implements `OpenAPISchema()`; the build fails until it does, so it is never described wrongly.
- **Every scenario has an example.** The docs show each success and each failure as a complete response body, built the way `response.Success` and `response.Error` build them. Request and data examples come from the values written in `Docs`, or from an explicit JSON example; both are checked against the schema, so an example cannot drift from its type.
- **Every header is listed:** the ones a route reads (`Authorization` through its scheme, `X-Request-ID`, the service's own), and the ones every response carries (`X-Request-ID` and the security headers).
- **Coverage is checked.** `api.Spec` mounts every handler exactly as `NewRouter` does and fails when a mounted route has no docs or documented route is not mounted. Handlers are built in `internal/app/routes.go` by one `routes(services)` function that `New` and `OpenAPI` share; the spec is built with zero services, which works because a handler's constructor only stores what it is given and never calls it.
- **Each branch carries its own spec.** `make openapi` writes `docs/openapi.json`; `internal/app/openapi_test.go` fails `make verify` when the committed file differs from the code. Checking out a branch or a release gives that branch's exact API.
- **A service never serves its spec.** The docs portal reads `docs/openapi.json` from git, behind company sign-in. Serving it from the API would publish a map of every route to anyone who can reach the service.

## Testing strategy

| Layer | How | Speed |
|---|---|---|
| domain | table-driven unit tests with in-memory fakes of the ports | ms |
| storage | integration tests against a real database, behind the `integration` build tag: the database comes from `<STORE>_TEST_URI` (a local server, a CI service container or testcontainers) and the tests skip when it is unset; each test uses its own database and drops it | s |
| contract | golden files in `testdata/contract`: the producer's tests encode them, the consumer's decode them; identical copies in both repos | ms |
| api | `httptest` through the real router and middleware (see `internal/api/router_test.go`) | ms |
| api docs | `docs/openapi.json` equals what the handlers document (`internal/app/openapi_test.go`); `api.Spec` refuses undocumented routes | ms |
| app | boots the process, probes it and shuts it down (see `internal/app/app_test.go`) | s |

`make verify` (gofmt, vet, module integrity, race tests, build), `make lint` (including gosec) and `make vuln` (govulncheck) must pass before merge.
