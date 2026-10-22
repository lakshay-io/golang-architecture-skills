# goservice

Go backend service built on the team's standard architecture: Gin over HTTP (or AWS Lambda behind API Gateway), a ports-and-adapters layout, New Relic monitoring, structured logging, graceful lifecycle and OWASP-aligned defaults.

## Quick start

```sh
cp .env.example .env
make run                         # http://localhost:8080
curl -i localhost:8080/readyz    # 200 once started
```

Requires Go 1.27+. An older local Go downloads the right toolchain automatically.

## Make targets

| Target | What it does |
|---|---|
| `make run` | Run locally with `.env` |
| `make test` | All tests with the race detector |
| `make openapi` | Regenerate `docs/openapi.json` from the handlers' `Docs` (commit it) |
| `make verify` | gofmt, vet, module integrity, race tests (including a current `docs/openapi.json`), build |
| `make lint` | golangci-lint (pinned; includes gosec) |
| `make vuln` | govulncheck: known vulnerabilities the code can reach |
| `make build` | Static binary in `bin/` |
| `make docker` | Container image (distroless, non-root) |

CI runs `make verify lint vuln`; all three must pass before merge.

## Layout

```
cmd/server/          entrypoint
cmd/openapi/         writes docs/openapi.json
internal/app/        composition root: wiring and lifecycle
internal/config/     typed env config
internal/api/        Gin transport: router, middleware, response, handlers, apidoc (OpenAPI)
internal/domain/     business modules (one package per bounded context)
internal/storage/    adapters for databases and caches
internal/platform/   apperr, logger, telemetry (New Relic), httpclient, health, requestid
```

## Docs

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): the rules and why they exist.
- [docs/ADDING_A_MODULE.md](docs/ADDING_A_MODULE.md): the shape of a new business module.
- [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md): New Relic, logs and tracing.
- [docs/SECURITY.md](docs/SECURITY.md): the OWASP Top 10 and API Security Top 10, what the base covers and what every module must do.
- [docs/openapi.json](docs/openapi.json): the API as OpenAPI 3.1, generated from the handlers (`make openapi`): every route, header, body and response, each scenario with an example.

## Running in production

- **Probes:** liveness `GET /healthz`; readiness `GET /readyz`, which turns 503 while the service is draining or a dependency check fails.
- **Graceful shutdown:** the process handles SIGTERM. Set the platform's kill grace period above `SHUTDOWN_TIMEOUT` (default 25s).
- **Environment:** set `APP_ENV`, `NEW_RELIC_LICENSE_KEY` (required in production; New Relic runs only there) and `HTTP_TRUSTED_PROXIES` (your load balancer's range).
- **AWS Lambda:** the same binary serves API Gateway HTTP API events when the runtime sets `AWS_LAMBDA_FUNCTION_NAME`; build for `provided.al2023`.
- **Memory:** set `GOMEMLIMIT` to about 90% of the container memory limit. `GOMAXPROCS` follows the CPU limit automatically.
- **Config reference:** every setting is in [.env.example](.env.example).
