# Adding a domain module

A *module* is one business capability (a bounded context), such as orders, policies or devices. Every module has the same shape, so a reviewer can find anything in any service. The running example is `order`.

## Files

```
internal/domain/order/
  order.go          entity, invariants, sentinel errors
  ports.go          interfaces the module needs (Repository, publishers…)
  service.go        use cases
  service_test.go   table-driven tests with in-memory fakes of the ports
internal/storage/postgres/
  order_repo.go     adapter implementing order.Repository
internal/api/handler/
  order.go          wire DTOs, Register, handlers
  order_test.go     httptest through the real router
internal/app/app.go wiring: about three lines
```

## 1. Domain: `internal/domain/order`

```go
// order.go
package order

// Stable codes: clients switch on them, so never rename one.
var (
	ErrNotFound        = apperr.NotFound("order_not_found", "order not found")
	ErrVersionConflict = apperr.Conflict("order_version_conflict", "order was modified; reload and retry")
	ErrInvalid         = apperr.Invalid("order_invalid", "order is invalid")
)

type Order struct {
	ID        string
	// … business fields …
	Version   int64 // optimistic concurrency: bumped on every write
	CreatedAt time.Time
	UpdatedAt time.Time
}

// validate enforces invariants. Every write path calls it, so an Order that
// exists is always valid.
func (o Order) validate() error {
	details := map[string]string{}
	// if … { details["field"] = "reason" }
	if len(details) > 0 {
		return ErrInvalid.WithDetails(details)
	}
	return nil
}
```

```go
// ports.go: declared here by the consumer, implemented in internal/storage.
type Repository interface {
	Create(ctx context.Context, o Order) error
	Get(ctx context.Context, id string) (Order, error)                // ErrNotFound when missing
	Update(ctx context.Context, o Order, expectedVersion int64) error // ErrVersionConflict when stale
}
```

```go
// service.go
type Service struct {
	repo  Repository
	newID func() string    // injected so tests are deterministic
	now   func() time.Time // injected so tests are deterministic
}

func NewService(repo Repository, newID func() string, now func() time.Time) *Service {
	return &Service{repo: repo, newID: newID, now: now}
}

func (s *Service) Get(ctx context.Context, id string) (Order, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return Order{}, fmt.Errorf("get order %s: %w", id, err)
	}
	return o, nil
}
```

**Rules:**
- Imports: the standard library and `platform/apperr` only.
- Pass `ctx` everywhere.
- Inject time and IDs; never call `time.Now()` or generate IDs inline.

## 2. Storage adapter: `internal/storage/<driver>`

The adapter translates driver errors into the domain's errors and attaches New Relic datastore segments through the driver's integration. The domain never sees either.

```go
// internal/storage/postgres/postgres.go: one pool per process, created in app.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 20                         // replicas × MaxConns must stay below max_connections
	cfg.ConnConfig.Tracer = nrpgx5.NewTracer() // New Relic datastore segments
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
```

```go
// internal/storage/postgres/order_repo.go
func (r *OrderRepo) Get(ctx context.Context, id string) (order.Order, error) {
	var o order.Order
	err := r.pool.QueryRow(ctx,
		`SELECT id, version, created_at, updated_at FROM orders WHERE id = $1`, id,
	).Scan(&o.ID, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return order.Order{}, order.ErrNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("select order: %w", err)
	}
	return o, nil
}

// Compare-and-swap: the write lands only if nobody changed the row since it was read.
func (r *OrderRepo) Update(ctx context.Context, o order.Order, expected int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET version = $3, updated_at = $4 WHERE id = $1 AND version = $2`,
		o.ID, expected, o.Version, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return order.ErrVersionConflict
	}
	return nil
}
```

New Relic hooks for other drivers:

| Driver | Integration |
|---|---|
| MongoDB (mongo-driver v2) | `options.Client().SetMonitor(nrmongo.NewCommandMonitor(nil))`, from `integrations/nrmongo-v2` |
| Redis (go-redis v9) | `rdb.AddHook(nrredis.NewHook(opts))`, from `integrations/nrredis-v9` |
| gRPC | interceptors from `integrations/nrgrpc` |
| Outbound HTTP | `platform/httpclient.New(timeout)`: already instrumented |

Any adapter that owns connections also implements `health.Checker` (`Name()`, and `Check(ctx)` that pings) and gets a closer in `app`.

## 3. Handler: `internal/api/handler/order.go`

```go
// The slice of the service this handler needs, declared here by the consumer.
type OrderService interface {
	Get(ctx context.Context, id string) (order.Order, error)
}

type OrderHandler struct{ svc OrderService }

func NewOrderHandler(svc OrderService) *OrderHandler { return &OrderHandler{svc: svc} }

func (h *OrderHandler) Register(r gin.IRouter) {
	g := r.Group("/orders")
	g.GET("/:id", Wrap(h.get))
}

// The public contract: separate from order.Order on purpose.
type orderResponse struct {
	ID        string    `json:"id"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (h *OrderHandler) get(c *gin.Context) error {
	o, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		return err // response.Error maps ErrNotFound → 404 with code order_not_found
	}
	// The standard body: {"success":true,"status":200,"code":"order_found","message":…,"data":{…},"requestId":…}
	response.Success(c, http.StatusOK, "order_found", "Order found", orderResponse{ID: o.ID, Version: o.Version, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt})
	return nil
}
```

Request bodies use `BindJSON(c, &req)` with binding tags for wire shape only, e.g. `binding:"required,gt=0"`. Business rules stay in the domain.

## 4. Wiring: `internal/app/app.go`

```go
pool, err := postgres.Open(ctx, cfg.Postgres.DSN)
checks = append(checks, postgres.NewChecker(pool))
a.onShutdown("postgres", func(context.Context) error { pool.Close(); return nil })

orders := order.NewService(postgres.NewOrderRepo(pool),
	func() string { return uuid.Must(uuid.NewV7()).String() }, // github.com/google/uuid
	time.Now)

V1: []api.Registrar{handler.NewOrderHandler(orders)},
```

New settings, such as the DSN, go into `internal/config` (typed, validated, and documented in `.env.example`).

If the adapter relies on schema the store must already have (a unique index that keeps writes correct, a table, a time-series collection), add it to `cmd/migrate` and check it at startup; see "Schema and migrations" in ARCHITECTURE.md.

If the module must react to another module, do not import it: declare the port it needs (`ChangeNotifier`, say) and let `app` wire an adapter that calls the other module (ARCHITECTURE.md, dependency rule 6).

## Checklist

- [ ] `internal/domain/order` imports only the standard library and `platform/apperr`.
- [ ] Errors are `apperr` sentinels with stable codes; adapters map driver errors to them.
- [ ] `ctx` flows from `c.Request.Context()` through the service into every query or call.
- [ ] Wire DTOs are separate from the entity; times are RFC 3339 UTC; fields are camelCase.
- [ ] Successes render through `response.Success` with a stable `code`; failures `return err`. No handler calls `c.JSON`.
- [ ] The datastore uses its New Relic integration; outbound HTTP uses `platform/httpclient`.
- [ ] Adapters with connections register a readiness check and a closer.
- [ ] Wiring happens only in `internal/app`.
- [ ] Domain unit tests, handler tests through the router, and storage integration tests (`-tags integration`) exist.
- [ ] It imports no other module; cooperation goes through a port wired in `app`.
- [ ] Schema the adapter relies on is created by `cmd/migrate` and checked at startup.
- [ ] Security (docs/SECURITY.md): every query filters on the owner taken from the authenticated caller, with a cross-tenant test that gets 404; every route group has its role's auth middleware; DTOs carry only what that caller may set and see; every list and array has a maximum; no secret, token or personal data is logged.
- [ ] `make verify lint vuln` passes.
