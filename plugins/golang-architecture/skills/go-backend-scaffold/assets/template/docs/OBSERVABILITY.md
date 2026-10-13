# Observability (New Relic)

New Relic is the monitoring backend. Setup lives in `internal/platform/telemetry`. Request instrumentation lives in `internal/api/middleware/newrelic.go`.

## What you get without writing code

| Signal | How |
|---|---|
| One transaction per endpoint | Named by route template, e.g. `GET /v1/orders/:id`, not by raw URL. |
| Distributed tracing | Inbound trace headers are accepted. Outbound calls through `platform/httpclient` propagate them. |
| Logs in context | Every log line written with a request `ctx` carries `trace.id`/`span.id` and is forwarded, so the logs for a trace are one click away. |
| Errors with real causes | 5xx responses are reported with the actual Go error chain via `NoticeError`, not a bare "500". Panics are recorded. |
| Client errors kept out of alerts | 4xx are collected as *expected* errors: visible, but they don't move the error rate, Apdex or alerts. |
| `request_id` attribute | On every transaction, so you can go from a trace to a support ticket and back. |
| Labels | `env` and `version` on all data; add more with `NEW_RELIC_LABELS`. |
| Go runtime metrics | Goroutines, GC pauses and memory are collected by the agent. |

Health probes and unmatched paths (404 scans) are **not** monitored. Probes hit every pod every few seconds and would drown real traffic in throughput and Apdex.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `NEW_RELIC_LICENSE_KEY` | — | Setting it turns monitoring on. **Required in production** (boot fails otherwise). |
| `NEW_RELIC_APP_NAME` | `<SERVICE_NAME>-<APP_ENV>` | One app per environment, so staging never pollutes production dashboards. |
| `NEW_RELIC_ENABLED` | on when a key is set | Force on or off. |
| `NEW_RELIC_APPLICATION_LOGGING_FORWARDING_ENABLED` | `true` | Set `false` if stdout is already shipped to New Relic (Fluent Bit, the Kubernetes integration); otherwise every line arrives twice. |
| any other `NEW_RELIC_*` | agent default | Read by the agent directly: labels, high security, proxy, profiling. |

Locally, with no key, the agent is inert. Code paths are identical, so nothing needs an "if monitoring" branch.

## Instrumenting new code

The agent is reached through the request context (`newrelic.FromContext(ctx)`) and only at the edges: middleware, storage adapters and outbound clients. **Domain packages never import New Relic.**

**Datastores.** Use the driver's integration when you create the client:

| Driver | Integration |
|---|---|
| pgx v5 | `cfg.ConnConfig.Tracer = nrpgx5.NewTracer()` |
| MongoDB v2 | `options.Client().SetMonitor(nrmongo.NewCommandMonitor(nil))` (module `nrmongo-v2`) |
| go-redis v9 | `rdb.AddHook(nrredis.NewHook(opts))` (module `nrredis-v9`) |

Queries made with the request `ctx` appear as datastore segments in the trace.

**Outbound HTTP.** Build the client with `httpclient.New(timeout)` and the request with `http.NewRequestWithContext(ctx, …)`. That gives you an external segment, distributed-tracing headers and `X-Request-ID` forwarding.

**Expensive in-process work.** Add a custom segment in the adapter or handler that calls it:

```go
defer newrelic.FromContext(ctx).StartSegment("pricing.recalculate").End()
```

**Goroutines inside a request.** Each goroutine that uses the transaction needs its own handle:

```go
txn := newrelic.FromContext(ctx)
go func(txn *newrelic.Transaction) {
	defer txn.End() // ends only this goroutine's segment tree
	work(newrelic.NewContext(ctx, txn))
}(txn.NewGoroutine())
```

**Background jobs, consumers and schedulers.** These are non-web transactions, one per unit of work:

```go
txn := nrApp.StartTransaction("job/reconcile-policies")
defer txn.End()
ctx = newrelic.NewContext(ctx, txn)
if err := run(ctx); err != nil {
	txn.NoticeError(err)
}
```

**Custom attributes.** Add them in handlers, which know the request: `newrelic.FromContext(ctx).AddAttribute("tenant_id", id)`. Use IDs only, never personal data or secrets.

## Suggested alerts

- **Error rate:** 5xx above 1% for 5 minutes, per transaction.
- **Latency:** p95 above the endpoint's budget for 10 minutes.
- **Throughput:** sudden drop (the service is unreachable or upstream is broken).
- **Apdex:** below 0.9.
- **Saturation:** goroutine count or GC pause trending up (leaks, overload).
