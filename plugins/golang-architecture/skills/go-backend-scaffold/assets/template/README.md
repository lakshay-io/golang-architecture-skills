# goservice

Go backend service built on the team's standard architecture: Gin over HTTP, a ports-and-adapters layout, New Relic monitoring, structured logging and graceful lifecycle.

## Quick start

```sh
cp .env.example .env
make run                         # http://localhost:8080
curl -i localhost:8080/readyz    # 200 once started
```

Requires Go 1.26+. An older local Go downloads the right toolchain automatically.

## Make targets

| Target | What it does |
|---|---|
| `make run` | Run locally with `.env` |
| `make test` | All tests with the race detector |
| `make verify` | What CI runs: gofmt, vet, module integrity, race tests, build |
| `make lint` | golangci-lint v2 |
| `make build` | Static binary in `bin/` |
| `make docker` | Container image (distroless, non-root) |

## Layout

```
cmd/server/          entrypoint
internal/app/        composition root: wiring and lifecycle
internal/config/     typed env config
internal/api/        Gin transport: router, middleware, response, handlers
internal/domain/     business modules (one package per bounded context)
internal/storage/    adapters for databases and caches
internal/platform/   apperr, logger, telemetry (New Relic), httpclient, health, requestid
```

## Docs

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): the rules and why they exist.
- [docs/ADDING_A_MODULE.md](docs/ADDING_A_MODULE.md): the shape of a new business module.
- [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md): New Relic, logs and tracing.

## Running in production

- **Probes:** liveness `GET /healthz`; readiness `GET /readyz`, which turns 503 while the service is draining or a dependency check fails.
- **Graceful shutdown:** the process handles SIGTERM. Set the platform's kill grace period above `SHUTDOWN_TIMEOUT` (default 25s).
- **Environment:** set `APP_ENV`, `NEW_RELIC_LICENSE_KEY` (required in production) and `HTTP_TRUSTED_PROXIES` (your load balancer's range).
- **Memory:** set `GOMEMLIMIT` to about 90% of the container memory limit. `GOMAXPROCS` follows the CPU limit automatically.
- **Config reference:** every setting is in [.env.example](.env.example).
