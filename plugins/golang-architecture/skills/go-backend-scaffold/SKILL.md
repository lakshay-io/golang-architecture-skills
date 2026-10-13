---
name: go-backend-scaffold
description: Create the standard production architecture for a Go (Golang) backend service — Gin HTTP transport, ports-and-adapters layout, New Relic monitoring, slog structured logging, typed env config, graceful shutdown, health probes, error envelope, Makefile/Dockerfile/lint — as a compiling, tested base with no business features. Use this whenever the user wants to start a new Go backend, service, microservice or API; asks for Go project structure, boilerplate, skeleton, base code or "the architecture"; asks how a Go service should be laid out; wants to add a new domain module to a service built from this base; or wants an existing Go service brought in line with the team standard — even if they never say "scaffold".
---

# Go backend scaffold

Produces the team's standard Go backend **architecture**: the skeleton every service shares. It is a real, compiling module whose tests pass under `-race`. It contains infrastructure only and no business features.

- **Stack:** Go 1.26+, Gin, New Relic (`go-agent/v3` with `nrgin` and `nrslog`), `log/slog`.
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
   - The first run may download the Go 1.26 toolchain and modules, which needs network access.
   - Never hand over a red build. If something fails, fix the cause.
4. **Report back briefly:**
   - the tree;
   - `make run` and `/readyz`;
   - where the docs are (`docs/ARCHITECTURE.md`, `docs/ADDING_A_MODULE.md`, `docs/OBSERVABILITY.md`);
   - that New Relic turns on when `NEW_RELIC_LICENSE_KEY` is set and is required when `APP_ENV=production`.

**Scope:** the scaffold is architecture only. Do not add domain modules, endpoints, database adapters or business logic unless the user asks for them. The empty `V1` route list in `internal/app/app.go` is intentional.

## Adding a module (a feature area) to a scaffolded service

Follow `docs/ADDING_A_MODULE.md` inside the project. Every scaffolded service ships it, and the template copy is at `${CLAUDE_SKILL_DIR}/assets/template/docs/ADDING_A_MODULE.md`. In short:

- **domain package:** entity, invariants, `apperr` sentinels, `ports.go` interfaces, `Service`;
- **storage adapter:** maps driver errors to domain errors and uses the driver's New Relic integration;
- **handler:** wire DTOs, `Register`, bind → service → render;
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
5. Copy individual template files (`internal/platform/*`, middleware, `response`) when they fit.

## The architecture on one screen

```
cmd/server/            entry: signals, config, exit code
internal/app/          composition root: the ONLY place that knows concrete types; lifecycle
internal/config/       typed env config, validated at boot (all errors at once)
internal/api/          Gin: router.go (middleware order, /v1 mounting, http.Server),
                       middleware/, response/ (error envelope), handler/ (one file per module)
internal/domain/<m>/   entities, invariants, Service, ports — stdlib + platform/apperr ONLY
internal/storage/<d>/  adapters implementing domain ports (+ New Relic driver integration)
internal/platform/     apperr, logger, telemetry (New Relic), httpclient, health, requestid
```

Rules that matter most. The full reasoning is in `${CLAUDE_SKILL_DIR}/assets/template/docs/ARCHITECTURE.md`; read it when a request touches structure.

- **The domain never imports Gin, New Relic or drivers.** Interfaces are declared by their consumer. Wiring is constructor injection in `app`: no globals, no `init()`.
- **Middleware order:** RequestID → New Relic → AccessLog → Recovery → SecureHeaders → BodyLimit → Timeout.
  - New Relic must come before AccessLog, because the agent drops logs recorded after a transaction ends.
- **Handlers `return err`.** `handler.Wrap` sends it to `response.Error`, the single source of the error envelope `{"error":{code,message,details,request_id}}`.
  - It maps `apperr.Kind` to a status.
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
