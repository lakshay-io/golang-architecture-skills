---
name: go-backend-scaffold
description: Create the standard production architecture for a Go (Golang) backend service — Gin HTTP transport (also on AWS Lambda behind API Gateway), ports-and-adapters layout, New Relic monitoring, slog structured logging, typed env config, graceful shutdown, health probes, one response body for every status, OWASP-aligned security defaults, an OpenAPI 3.1 spec generated from the handlers (docs/openapi.json, every route, header, body and response scenario with examples, kept current by make verify), Makefile/Dockerfile/lint/govulncheck — as a compiling, tested base with no business features. It also holds the rules for every change in a service built from this base (one with internal/app, internal/api/response and internal/platform/apperr): load it before editing such a service, since services carry no CLAUDE.md. Use this whenever the user wants to start a new Go backend, service, microservice or API; asks for Go project structure, boilerplate, skeleton, base code or "the architecture"; asks how a Go service should be laid out; wants to add a new domain module or route to a service built from this base; wants a service's API documented, its OpenAPI or Swagger spec, or its docs updated; or wants an existing Go service brought in line with the team standard — even if they never say "scaffold".
---

# Go backend scaffold

Produces the team's standard Go backend **architecture**: the skeleton every service shares. It is a real, compiling module whose tests pass under `-race`. It contains infrastructure only and no business features.

- **Stack:** Go 1.27+, Gin, New Relic (`go-agent/v3` with `nrgin` and `nrslog`), `log/slog`; the same binary serves HTTP or AWS Lambda (API Gateway HTTP API).
- **Layout:** ports and adapters with a strict dependency rule.
- **Security:** OWASP API Security Top 10 and Top 10 mapped in `docs/SECURITY.md`; `make lint` runs gosec, `make vuln` runs govulncheck.
- **API docs:** `docs/openapi.json` (OpenAPI 3.1) is generated from every handler's `Docs` and DTO types by `make openapi`, and `make verify` fails when it is stale, so each branch carries its own spec.

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
   - where the docs are (`docs/ARCHITECTURE.md`, `docs/ADDING_A_MODULE.md`, `docs/OBSERVABILITY.md`, `docs/SECURITY.md`), and that `docs/openapi.json` is the generated API spec (`make openapi`);
   - that New Relic runs only when `APP_ENV=production`, where `NEW_RELIC_LICENSE_KEY` is required;
   - that the same binary serves API Gateway events when deployed to AWS Lambda.

**Scope:** the scaffold is architecture only. Do not add domain modules, endpoints, database adapters or business logic unless the user asks for them. The empty `V1` route list in `internal/app/routes.go` is intentional.

## Rules for every change in a service built on this base

Services built from this base carry no `CLAUDE.md`: these rules live here, so every service follows the same version of them. Read the service's `docs/ARCHITECTURE.md` before structural changes, and follow `docs/ADDING_A_MODULE.md` to add a business capability.

- `internal/domain/**` imports only the standard library and `internal/platform/apperr`. Never Gin, New Relic or a database driver.
- Only `internal/app` constructs concrete types. Wiring is constructor injection: no globals, no `init()`.
- Handlers follow bind → service(`c.Request.Context()`) → render. Never write a body by hand: every success goes through `response.Success` and every failure is `return err`, which reaches `response.Error` (New Relic error reporting depends on it). No handler calls `c.JSON`.
- Pass `ctx` into every I/O call. Never let `*gin.Context` escape a handler.
- Outbound HTTP uses `platform/httpclient`. Datastores use their New Relic integration (see `docs/OBSERVABILITY.md`).
- New settings go through `internal/config` (typed, validated) and `.env.example`. Nothing else reads environment variables.
- JSON fields are camelCase; times are RFC 3339 UTC.
- Modules do not import each other: the module that acts declares a port, and `app` wires an adapter to the other module. Only a module defining a shared plug-in contract may be imported, by its plug-ins, one way.
- Schema an adapter relies on (unique indexes, tables, time-series collections) is created by `cmd/migrate` and checked at startup. Other binaries live in `cmd/<tool>` and call one `app` function each.
- `make verify`, `make lint` and `make vuln` must pass.

## API documentation rules

Every route is documented where it is mounted; `docs/ARCHITECTURE.md` ("API documentation") has the reasoning.

- Every handler has `Docs() apidoc.Group` beside `Register`, listing op for op the routes `Register` mounts. `api.Spec` fails the build on a mounted route without an op, or an op without a route.
- Each op gives: `Summary` (and `Description` when there is more to say), its auth (the group's `Auth`, an op's own, or `apidoc.Public`), every header it reads, path and query parameters with an `Example`, the `Body`, every `Success` and every `Error` the route itself returns, each with its stable `Code`, its real `Message` and `When` it happens, `Details` for a failure about fields. The standard failures (bad JSON, the auth scheme's, 500 `internal`, 504 `timeout`) are added by the build; a code the route lists itself replaces the standard description of it. Several outcomes at one status (`downtime_activated`, `downtime_deactivated`) are separate `Success` entries.
- Every scenario a client can meet gets its own example: list a code once per scenario with a `When` that tells them apart (a list with rows and an empty one; a 409 whose message depends on the cause). Each becomes a named example, labelled `code: When`.
- The values in `Docs` are the examples frontend developers copy: fill `Body` and `Data` with realistic data, never with real users' data. A DTO holding a type with its own JSON methods gives `Example` as JSON instead; every example is checked against its schema.
- A type with a custom `UnmarshalJSON`/`MarshalJSON` implements `OpenAPISchema() apidoc.Schema` describing the JSON it really accepts. A field the domain requires, where no binding tag says so, gets `api:"required"`; `doc:"…"` describes a field.
- One gin route serving several shapes (`/telemetry/:type`) is documented as one op per concrete value (`Path: "/telemetry/battery", Route: "/telemetry/:type"`), its body `apidoc.Fields(sharedDTO, "field", …).Require(…)`. Variants of one shared DTO in a `OneOf` fix their discriminator with `.Const("type", "SCREEN_FREE_START")`, so each has its own shape and example.
- Handlers are listed in `internal/app/routes.go` (`routes(services)`), shared by `New` and `OpenAPI`. A handler's constructor only stores its arguments: the spec builds every handler with zero services. Routes mounted only for `APP_ENV=local` are appended in `New`, outside `routes`, and stay out of the spec.
- `internal/app/openapi.go` holds the service's `apiInfo`: title, servers (local and staging, never production), and its token schemes with the failures their middleware returns.
- After any change to a route, a DTO or a `Docs`, run `make openapi` and commit `docs/openapi.json`. `make verify` fails while it is stale.
- Never serve the spec from the service; the docs portal reads it from git.

## Security rules (OWASP)

`docs/SECURITY.md` in every service maps the OWASP API Security Top 10 (2023) and Top 10 to the base. Apply it to every change; its checklist is part of every module's review.

- **Object-level authorization (API1):** every query that reads or changes an object filters on its owner, taken from the authenticated caller, never from the request. Another tenant's object answers exactly like a missing one (404). Every module has a cross-tenant test.
- **Function-level authorization (API5):** every route group mounts the auth middleware of the one role that may call it. Only the probes are public. Dev-only routes are mounted only for `APP_ENV=local`, in `internal/app`.
- **Authentication (API2):** pin the token algorithm; check expiry or the session behind the token on every request; one 401 body for every bad token; 503, never 401, when the account store is down.
- **Property-level authorization (API3):** handler-owned DTOs only, decoded strictly by `handler.BindJSON` (`encoding/json/v2`: unknown fields, duplicate keys, other-case keys and trailing data refused). Never bind or render a domain or storage struct.
- **Resource consumption (API4):** body and time limits are in the base; every list, page and array in a body has a maximum; gzip is capped after decompression; fan-out is bounded; rate limits sit at the edge.
- **Injection (A03):** placeholders for SQL; Mongo filters built from typed values, never from a decoded map; values that become a path or key are checked against the characters the store treats specially.
- **SSRF (API7):** never fetch a URL from a request.
- **Logging (A09):** never log secrets, tokens or personal data; secret config types implement `slog.LogValuer`.
- **Components (A06):** a reachable vulnerability `make vuln` reports is fixed before merge.

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

1. Render the template for the service's module path into a scratch directory (`scaffold.py <tmp> --module <module> --name <service>`) and diff every file against the service's copy. Infrastructure files (`internal/platform/*`, `internal/api/response`, `internal/api/apidoc/*`, `internal/api/{openapi,openapi_test}.go`, `internal/api/middleware/{middleware,newrelic}.go`, `internal/api/handler/{bind,handler,health}.go`, `internal/api/router_test.go`, `cmd/openapi`, `docs/{ARCHITECTURE,ADDING_A_MODULE,OBSERVABILITY,SECURITY}.md`) should end up identical apart from the module path; service-specific code (`internal/app`, `internal/config`, `Makefile`, `.env.example`, `README.md`, domain, storage, handlers, the service's own docs) takes only the change, merged by hand.
2. Update every handler and test the change touches (for example, a new response helper means every `c.JSON` becomes `response.Success`).
3. Delete a `CLAUDE.md` left by plugin 1.0.x: its rules are in this skill now.
   From 1.2.x to 1.3.0: move the handler list from `app.New` into `internal/app/routes.go` (`services` + `routes`, keeping each feature's enable condition in `New`), add `internal/app/{openapi,openapi_test}.go` with the service's servers and token schemes, write `Docs` for every handler (texts from the service's existing API docs, checked against the code's codes and messages), implement `OpenAPISchema` on every custom JSON type, then `make openapi`.
4. Run `make verify lint vuln` in each service, plus its integration tests. Toolchain or dependency upgrades in the template (`go.mod`, `Dockerfile`) are separate steps: ask before taking them into a deployed service. A reachable vulnerability from `make vuln` is fixed in the same sync.

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
3. Audit it against `${CLAUDE_SKILL_DIR}/assets/template/docs/SECURITY.md` too, and run `govulncheck`.
4. Report the gaps ordered by production risk:
   - security: authorization, authentication, reachable vulnerabilities, injection, exposed internals;
   - missing timeouts, graceful shutdown or context propagation;
   - error handling and New Relic coverage;
   - layering violations;
   - style.
5. Propose an incremental migration in which each step keeps the service deployable.
6. Copy individual template files (`internal/platform/*`, middleware, `response`) when they fit. Moving to the standard response body changes what clients parse: agree on it with the client teams first.

## The architecture on one screen

```
cmd/server/            entry: signals, config, exit code
cmd/openapi/           writes docs/openapi.json (make openapi)
cmd/<tool>/            migrate, dev and ops tools: each calls one app.<Root>
internal/app/          composition root: the ONLY place that knows concrete types; lifecycle; HTTP or Lambda;
                       routes.go (the handler list), openapi.go (the API's servers and token schemes)
internal/config/       typed env config, validated at boot (all errors at once)
internal/api/          Gin: router.go (middleware order, /v1 mounting, http.Server), openapi.go (Spec),
                       middleware/, response/ (the one response body), handler/ (one file per module, with Docs),
                       apidoc/ (OpenAPI 3.1 from Docs and DTO types)
internal/<transport>/  other triggers (e.g. trigger/ for Kinesis/SQS events): decode → service → errors
internal/domain/<m>/   entities, invariants, Service, ports — stdlib + platform/apperr ONLY
internal/storage/<d>/  adapters implementing domain ports (+ New Relic driver integration)
internal/platform/     apperr, logger, telemetry (New Relic), httpclient, health, requestid
docs/openapi.json      the generated API spec: committed, kept current by make verify
```

Rules that matter most. The full reasoning is in `${CLAUDE_SKILL_DIR}/assets/template/docs/ARCHITECTURE.md`; read it when a request touches structure.

- **The domain never imports Gin, New Relic or drivers.** Interfaces are declared by their consumer. Wiring is constructor injection in `app`: no globals, no `init()`.
- **Middleware order:** RequestID → New Relic → AccessLog → Recovery → SecureHeaders → BodyLimit → Timeout.
  - New Relic must come before AccessLog, because the agent drops logs recorded after a transaction ends.
  - SecureHeaders sets CSP, HSTS, `nosniff`, `X-Frame-Options`, `Referrer-Policy` and `no-store` (OWASP REST Security Cheat Sheet).
- **In AWS Lambda** (`AWS_LAMBDA_FUNCTION_NAME` set by the runtime), `app.Run` serves API Gateway HTTP API events through the same router via the Gin adapter; everything `app.New` builds is reused across invocations.
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
  - Monitoring runs only when `APP_ENV=production`, where the license key is required; local, test and staging keep the agent inert even with a key, so they never report into production dashboards.
  - The app name defaults to `<service>-<env>`.
  - Labels `env` and `version` are added.
- **Error policy:**
  - 4xx are *expected* errors: visible, but they don't move the error rate or alerts;
  - 5xx are reported via `NoticeError` with the real Go cause, and status-code-derived 5xx are ignored to avoid duplicates;
  - panics are recorded.
- **Transactions.** `middleware.NewRelic` runs `nrgin`, which names transactions by route, and skips probes and unmatched paths. It then copies the transaction into the request context, which `nrgin` alone does not do. That copy is what lets logs in context (`nrslog`), datastore segments and outbound tracing work.
- **Extending.** For new adapters, background jobs, goroutines and custom attributes, see `${CLAUDE_SKILL_DIR}/assets/template/docs/OBSERVABILITY.md`.
