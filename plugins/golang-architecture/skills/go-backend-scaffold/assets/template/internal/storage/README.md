# internal/storage

Adapters that implement domain ports, one package per technology, e.g. `postgres`, `mongo`, `redis`. Each adapter:

- **Maps errors.** It translates driver errors into the domain's errors (`pgx.ErrNoRows` → `order.ErrNotFound`, a failed compare-and-swap → `ErrVersionConflict`).
- **Maps types.** It converts between rows or documents and domain entities, so the schema can change without touching the domain.
- **Instruments itself** with the driver's New Relic integration (see [docs/OBSERVABILITY.md](../../docs/OBSERVABILITY.md)).
- **Plugs into the lifecycle.** If it owns connections, it implements `health.Checker` and registers a closer in `internal/app`.

Integration tests run against a real database and sit behind the `integration` build tag.
