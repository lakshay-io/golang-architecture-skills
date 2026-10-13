# internal/domain

One package per business module (bounded context), for example `internal/domain/order`. Each package holds:

- **Entities** with their invariants.
- **The use cases** (a `Service`).
- **Ports**: the interfaces the module needs, such as `Repository`, declared here and implemented in `internal/storage`.

Imports are limited to the standard library and `internal/platform/apperr`. That restriction keeps business rules fast to test and independent of frameworks and infrastructure.

The full pattern and checklist are in [docs/ADDING_A_MODULE.md](../../docs/ADDING_A_MODULE.md).
