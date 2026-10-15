---
name: go-backend-scaffold
description: Create the standard production architecture for a Go (Golang) backend service — Gin HTTP transport, ports-and-adapters layout, New Relic monitoring, slog structured logging, typed env config, graceful shutdown, health probes, one response body for every status, Makefile/Dockerfile/lint — as a compiling, tested base with no business features. It also holds the rules for every change in a service built from this base (one with internal/app, internal/api/response and internal/platform/apperr): load it before editing such a service, since services carry no CLAUDE.md. Use this whenever the user wants to start a new Go backend, service, microservice or API; asks for Go project structure, boilerplate, skeleton, base code or "the architecture"; asks how a Go service should be laid out; wants to add a new domain module to a service built from this base; or wants an existing Go service brought in line with the team standard — even if they never say "scaffold".
---

# Go backend scaffold

Produces the team's standard Go backend **architecture**: the skeleton every service shares. It is a real, compiling module whose tests pass under `-race`. It contains infrastructure only and no business features.

- **Stack:** Go 1.27+, Gin, New Relic (`go-agent/v3` with `nrgin` and `nrslog`), `log/slog`.
- **Layout:** ports and adapters with a strict dependency rule.

The architecture is fixed on purpose: every service looking the same is the point. Don't swap the framework, monitoring tool or layout per project unless the user explicitly asks.

## Creating a new service

1. **Settle three inputs, inferring before asking:**
   - **Destination.** The current directory is the usual target when it is a fresh repo (only `.git`, `README.md`, `LICENSE`…). Otherwise use a new directory named after the service.
   - **Module path.** Take it from `git remote get-url origin` (`git@github.com:org/repo.git` → `github.com/org/repo`). Ask only if there is no remote.
   - **Service name.** Defaults to the last element of the module path.
2. **Generate:**
   ```sh
   python3 "${CLAUDE_SKILL_DIR}/scripts/scaffold.py" <dest> --module <module-path> [--name <service-name>]
   ```
   Existing files are never overwritten; the script lists them. Merge those by hand. For an existing `README.md`, keep the user's description and add the template's Quick start, Layout and Docs sections under it.
3. **Verify.** Run `cd <dest> && make verify` (gofmt, vet, module integrity, race tests, build).
   - The first run may download the Go 1.27 toolchain and modules, which needs network access.
   - Never hand over a red build. If something fails, fix the cause.
4. **Report back briefly:**
   - the tree;
   - `make run` and `/readyz`;
   - where the docs are (`docs/ARCHITECTURE.md`, `docs/ADDING_A_MODULE.md`, `docs/OBSERVABILITY.md`);
   - that New Relic turns on when `NEW_RELIC_LICENSE_KEY` is set and is required when `APP_ENV=production`.

**Scope:** the scaffold is architecture only. Do not add domain modules, endpoints, database adapters or business logic unless the user asks for them. The empty `V1` route list in `internal/app/app.go` is intentional.

## Rules for every change in a service built on this base

Services built from this base carry no `CLAUDE.md`: these rules live here, so every service follows the same version of them. Read the service's `docs/ARCHITECTURE.md` before structural changes, and follow `docs/ADDING_A_MODULE.md` to add a business capability.

- `internal/domain/**` imports only the standard library and `internal/platform/apperr`. Never Gin, New Relic or a database driver.
- Only `internal/app` constructs concrete types. Wiring is constructor injection: no globals, no `init()`.
- Handlers follow bind → service(`c.Request.Context()`) → render. Never write a body by hand: every success goes through `response.Success` and every failure is `return err`, which reaches `response.Error` (New Relic error reporting depends on it). No handler calls `c.JSON`.
- Pass `ctx` into every I/O call. Never let `*gin.Context` escape a handler.
- Outbound HTTP uses `platform/httpclient`. Datastores use their New Relic integration (see `docs/OBSERVABILITY.md`).
- New settings go through `internal/config` (typed, validated) and `.env.example`. Nothing else reads environment variables.
- JSON fields are camelCase; times are RFC 3339 UTC.
- `make verify` must pass.

## One response body for every status

Every response, success or failure, in every service built on this base has the same shape, so a client parses all of them the same way:

```json
{ "success": true,  "status": 201, "code": "order_created", "message": "Order created",      "data": { "id": "…" },                     "requestId": "…" }
{ "success": false, "status": 400, "code": "order_invalid", "message": "Order is invalid",   "details": { "items[0].qty": "must be at least 1" }, "requestId": "…" }
{ "success": false, "status": 503, "code": "db_unavailable", "message": "Try again shortly", "requestId": "…" }
```

- **Always present:** `success` (true for 2xx), `status` (the HTTP status), `code` (stable and machine-readable; clients switch on it), `message` (human-readable; never parsed) and `requestId` (equal to the `X-Request-ID` header).
- **Only on success:** `data`, the result. `response.Success` renders a nil result as `{}`, so every success has it.
- **Only on failure about specific fields:** `details`, field path → problem.
- Probes use it too: `/healthz` answers `live`, `/readyz` answers `ready`, `not_ready` or `degraded` (with the failed checks in `details`).
- `internal/api/response/response_test.go` pins the shape; keep it passing.

## Syncing a service to a new plugin version

Existing services do not change when the plugin does: the scaffold never overwrites files. When the user asks to bring services up to a new version:

1. Diff each template file against the service's copy, reading the template's `example.com/goservice` as the service's module path. Infrastructure files (`internal/platform/*`, `internal/api/response`, `internal/api/middleware`, `internal/api/handler/{bind,handler,health}.go`, `docs/*`) should end up identical apart from the module path; service-specific code (`internal/app`, `internal/config`, domain, storage, handlers) takes only the change, merged by hand.
2. Update every handler and test the change touches (for example, a new response helper means every `c.JSON` becomes `response.Success`).
3. Delete a `CLAUDE.md` left by plugin 1.0.x: its rules are in this skill now.
4. Run `make verify` and lint in each service. Toolchain or dependency upgrades in the template (`go.mod`, `Dockerfile`) are separate steps: ask before taking them into a deployed service.

## Adding a module (a feature area) to a scaffolded service

Follow `docs/ADDING_A_MODULE.md` inside the project. Every scaffolded service ships it, and the template copy is at `${CLAUDE_SKILL_DIR}/assets/template/docs/ADDING_A_MODULE.md`. In short:

- **domain package:** entity, invariants, `apperr` sentinels, `ports.go` interfaces, `Service`;
- **storage adapter:** maps driver errors to domain errors and uses the driver's New Relic integration;
- **handler:** wire DTOs, `Register`, bind → service → `response.Success`;
- **wiring:** three lines in `internal/app/app.go`.

Then work through its checklist and run `make verify`.

## Bringing an existing Go service in line

Do not rewrite it wholesale.

1. Read `${CLAUDE_SKILL_DIR}/assets/template/docs/ARCHITECTURE.md`.
2. Audit the service against it.
3. Report the gaps ordered by production risk:
   - missing timeouts, graceful shutdown or context propagation;
   - error handling and New Relic coverage;
   - layering violations;
   - style.
4. Propose an incremental migration in which each step keeps the service deployable.
5. Copy individual template files (`internal/platform/*`, middleware, `response`) when they fit. Moving to the standard response body changes what clients parse: agree on it with the client teams first.

## The architecture on one screen

```
cmd/server/            entry: signals, config, exit code
internal/app/          composition root: the ONLY place that knows concrete types; lifecycle
internal/config/       typed env config, validated at boot (all errors at once)
internal/api/          Gin: router.go (middleware order, /v1 mounting, http.Server),
                       middleware/, response/ (the one response body), handler/ (one file per module)
internal/domain/<m>/   entities, invariants, Service, ports — stdlib + platform/apperr ONLY
internal/storage/<d>/  adapters implementing domain ports (+ New Relic driver integration)
internal/platform/     apperr, logger, telemetry (New Relic), httpclient, health, requestid
```

Rules that matter most. The full reasoning is in `${CLAUDE_SKILL_DIR}/assets/template/docs/ARCHITECTURE.md`; read it when a request touches structure.

- **The domain never imports Gin, New Relic or drivers.** Interfaces are declared by their consumer. Wiring is constructor injection in `app`: no globals, no `init()`.
- **Middleware order:** RequestID → New Relic → AccessLog → Recovery → SecureHeaders → BodyLimit → Timeout.
  - New Relic must come before AccessLog, because the agent drops logs recorded after a transaction ends.
- **Handlers render with `response.Success` and `return err`.** `handler.Wrap` sends the error to `response.Error`. Together they are the only writers of the response body `{success, status, code, message, data | details, requestId}`.
  - `response.Error` maps `apperr.Kind` to a status.
  - It hides internal errors from the client.
  - It records the cause for the access log and New Relic.
- **`ctx` from `c.Request.Context()` flows into every I/O call.** `*gin.Context` never escapes a handler.
- **Scalability defaults:**
  - stateless replicas;
  - timeouts everywhere;
  - `platform/httpclient` for outbound calls;
  - keyset pagination;
  - UUIDv7 IDs;
  - optimistic concurrency;
  - bounded concurrency and queues;
  - readiness drain on SIGTERM.

## New Relic integration (what is wired and why)

- **Agent setup.** `platform/telemetry.NewRelic` reads every standard `NEW_RELIC_*` variable.
  - Monitoring is on when a license key is set and mandatory when `APP_ENV=production`.
  - The app name defaults to `<service>-<env>`.
  - Labels `env` and `version` are added.
- **Error policy:**
  - 4xx are *expected* errors: visible, but they don't move the error rate or alerts;
  - 5xx are reported via `NoticeError` with the real Go cause, and status-code-derived 5xx are ignored to avoid duplicates;
  - panics are recorded.
- **Transactions.** `middleware.NewRelic` runs `nrgin`, which names transactions by route, and skips probes and unmatched paths. It then copies the transaction into the request context, which `nrgin` alone does not do. That copy is what lets logs in context (`nrslog`), datastore segments and outbound tracing work.
- **Extending.** For new adapters, background jobs, goroutines and custom attributes, see `${CLAUDE_SKILL_DIR}/assets/template/docs/OBSERVABILITY.md`.
