# Notes for Claude

This service follows the team's standard Go backend architecture. Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before structural changes. To add a business capability, follow [docs/ADDING_A_MODULE.md](docs/ADDING_A_MODULE.md).

Rules that are easy to break:

- `internal/domain/**` imports only the standard library and `internal/platform/apperr`. Never Gin, New Relic or a database driver.
- Only `internal/app` constructs concrete types. Wiring is constructor injection: no globals, no `init()`.
- Handlers follow bind → service(`c.Request.Context()`) → render, and `return err`. Never write an error body by hand: every error goes through `response.Error`, which New Relic error reporting depends on.
- Pass `ctx` into every I/O call. Never let `*gin.Context` escape a handler.
- Outbound HTTP uses `platform/httpclient`. Datastores use their New Relic integration (see docs/OBSERVABILITY.md).
- New settings go through `internal/config` (typed, validated) and `.env.example`. Nothing else reads environment variables.
- `make verify` must pass.
