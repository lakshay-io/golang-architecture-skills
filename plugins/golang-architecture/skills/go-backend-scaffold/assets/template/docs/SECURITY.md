# Security

Every service on this base is an API, so the checklist that matters is the **OWASP API Security Top 10 (2023)**, with the **OWASP Top 10** for what it adds. Each item below says what the base already does, with the file that does it, and what every module must do on top. A rule the base cannot enforce for you is a review item: the checklist at the end is what a reviewer goes through for every new module.

## OWASP API Security Top 10 (2023)

### API1 Broken Object Level Authorization

**Risk:** a caller reads or changes another tenant's object by sending its id.

- **Every module:** authorize the object, not just the route. The query that loads or changes an object filters on its owner, taken from the authenticated caller, never from the request: `WHERE id = $1 AND tenant_id = $2`, `{_id: id, ownerId: callerID}`. An object that exists but belongs to someone else answers exactly like one that does not exist (`404 <thing>_not_found`), so ids cannot be probed.
- **Never** trust an id, a tenant or a role in the body, a header or the path to say who the caller is. Identity comes from the verified token, through the auth middleware, into the domain as a typed value.
- **Tests:** every module has a test where caller A asks for caller B's object and gets 404.

### API2 Broken Authentication

**Risk:** forged, replayed or stolen tokens are accepted.

- **The base:** no authentication of its own: each service adds an auth middleware in `internal/api/middleware`, and its domain decides who the caller is.
- **Every service that verifies tokens:**
  - pins the signing algorithm (never trust the token's own `alg`: `alg=none` and RS256/HS256 confusion are classic forgeries);
  - checks expiry, or checks the session behind a token that has none, on every request, so a signed-out or revoked token stops working;
  - answers every bad token with the same 401 body, so a caller probing tokens learns nothing; the reason goes to the logged cause;
  - answers 503, never 401, when the account store is down: a client signs its user out on 401.
- Credentials, tokens and keys come from the secret store through `internal/config`, and their config types implement `slog.LogValuer` to redact them (see `NewRelicConfig`).

### API3 Broken Object Property Level Authorization

**Risk:** mass assignment (a client sets `isAdmin`) or excessive exposure (a response leaks fields the caller must not see).

- **The base:** request bodies are decoded into handler-owned DTOs with `encoding/json/v2` and `RejectUnknownMembers` (`internal/api/handler/bind.go`): an unknown field is `400 unknown_field`, a duplicate key `400 duplicate_field`, a key in another case is unknown. Wire types are not domain types (docs/ARCHITECTURE.md, rule 4).
- **Every module:** request DTOs hold only the fields a caller may set; response DTOs only the fields that caller may see. Never decode into, or render, a domain or storage struct.

### API4 Unrestricted Resource Consumption

**Risk:** a request, or many, exhausts memory, CPU, connections or money.

- **The base:**
  - bodies are capped (`HTTP_MAX_BODY_BYTES`, `middleware.BodyLimit`), and a gzip body must be capped again after decompression;
  - every request has a deadline (`HTTP_REQUEST_TIMEOUT`, `middleware.Timeout`) that cancels its database and outbound calls through `ctx`;
  - server timeouts bound slow clients (`HTTP_READ_HEADER_TIMEOUT` and the rest);
  - outbound calls have timeouts (`platform/httpclient`).
- **Every module:**
  - caps every list: page sizes, array lengths in bodies (`binding:"max=…"` or a domain limit), query results;
  - bounds fan-out and background work (`errgroup.SetLimit`, a bounded worker);
  - documents its limits in its API docs.
- **Every deployment:** rate limits per caller at the edge (API Gateway throttling, an ingress limit). An in-process limiter only limits one replica.

### API5 Broken Function Level Authorization

**Risk:** a caller reaches an operation meant for another role (a child token on a parent route, a user on an admin route).

- **Every module:** each route group mounts the middleware for exactly the role that may call it; there is no route without one except the probes. Roles are separate middlewares (`RequireParent`, `RequireChild`), not a flag checked inside handlers. Admin and internal routes live under their own group with their own middleware, never behind "hidden" paths.
- **Tests:** a token of the wrong role gets 401 or 403 on every route of the group.

### API6 Unrestricted Access to Sensitive Business Flows

**Risk:** a flow that is fine once is abused at scale (sign-ups, OTPs, purchases, invites).

- **Every module with such a flow:** limits it per account and per device in the domain or the shared store (counts with a window), not only per IP; makes it idempotent (a key, a unique constraint) so a retry or replay does one thing.

### API7 Server Side Request Forgery

**Risk:** the service fetches a URL a caller controls and reaches internal endpoints (cloud metadata, admin ports).

- **The base:** outbound calls go through `platform/httpclient` only.
- **Every module:** never fetch a URL from a request. When a caller must name a resource, take an id and map it to a URL you own. If a URL is unavoidable (webhooks), allow-list the scheme and host, resolve the host, refuse private, loopback and link-local addresses, and do not follow redirects.

### API8 Security Misconfiguration

**Risk:** defaults, debug output or missing headers expose the service.

- **The base:**
  - `middleware.SecureHeaders` sets `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`, `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and `Cache-Control: no-store` on every response;
  - Gin runs in release mode; panics are recovered into a 500 that says only "internal error" (`middleware.Recovery`, `response.Error`): no stack trace, driver message or query ever reaches a client;
  - config is validated at boot, unknown `APP_ENV` values refused, and production refuses to start without its required settings;
  - the container is distroless and non-root (`Dockerfile`).
- **Every service:** no CORS middleware unless a browser client needs one, and then an explicit allow-list of origins, never `*` with credentials. Debug or dev-only routes are mounted only when `APP_ENV=local`, decided in `internal/app`. `HTTP_TRUSTED_PROXIES` names only the load balancer, so a client cannot spoof its IP with `X-Forwarded-For`.

### API9 Improper Inventory Management

**Risk:** forgotten versions or undocumented routes stay exposed.

- **The base:** routes are mounted in one place (`internal/api/router.go`) under a version (`/v1`); `internal/app` logs every registered route at debug level. `docs/openapi.json` is the inventory: generated from every handler's `Docs`, refused by the build when a mounted route is undocumented, and kept current by `make verify`. It is read from git behind company sign-in, never served by the service.
- **Every service:** documents every route in its handler's `Docs` with its auth, and removes a route's old version once no client calls it. A path copied from another system (a legacy path behind a load balancer) is listed with the reason.

### API10 Unsafe Consumption of APIs

**Risk:** a third-party or internal API's response is trusted like our own data.

- **Every adapter that calls another API:** treats its responses as untrusted input: decodes into its own types, validates them, bounds their size, and maps its errors into domain errors (a 5xx from them is a 503 from us, never their message passed through). It uses TLS, a timeout, and a retry only when the call is idempotent.

## OWASP Top 10

The API list above covers access control, authentication, misconfiguration and SSRF (A01, A05, A07 and A10 in the 2021 edition). What it adds:

| Category | The base | Every module |
|---|---|---|
| **A02 Cryptographic failures** | TLS terminates at the load balancer or API Gateway; HSTS on every response. | Never invent crypto: use the standard library or the platform's KMS. Hash passwords with a slow KDF (argon2id, bcrypt), never a plain hash. Personal data in a datastore is encrypted at rest by the store. |
| **A03 Injection** | No query is ever built from strings in the base. | SQL uses placeholders, never `fmt.Sprintf`. A Mongo filter is built from typed values (`bson.M{"_id": id}`), never from a decoded map a client sent, so `{"$ne": null}` can never arrive as an operator. Values that become a path (a document id, a key, a file name) are checked against what the store allows (no `/`, `.`, `$`, `..`). |
| **A04 Insecure design** | Ports and adapters keep business rules testable on their own. | Write the abuse cases next to the use cases (API1, API6): who must not be able to do this, and the test that proves it. |
| **A06 Vulnerable and outdated components** | `make vuln` (govulncheck) reports known vulnerabilities the code can reach; `make lint` runs `gosec`; dependencies are pinned in `go.sum` and checked by `go mod verify`. | CI runs `make verify lint vuln` on every change. A reachable vulnerability is fixed before merge (upgrade the module); an unreachable one within the sprint. Keep the Go toolchain on a supported release. |
| **A08 Software and data integrity failures** | `go mod verify` checks every module against `go.sum`; the image builds from pinned versions. | Never decode untrusted data into types with behaviour (no gob or YAML into interfaces). Webhooks and messages from outside are signature-checked before use. |
| **A09 Security logging and monitoring failures** | One access-log line per request with the route, status, caller-free cause; 5xx reported to New Relic in production; request id on every line. | Log security-relevant events (failed authorization, a refused token, a limit hit) at `Warn` with the caller's id, never the token. Never log secrets, tokens or personal data (names, phone numbers, locations, message text). |

The 2025 edition adds **software supply chain failures** (covered by A06 and A08 above) and **mishandling of exceptional conditions**: every error goes through `response.Error`, which fails closed (an unknown error is a 500 that reveals nothing), and a handler never swallows an error to answer 200.

## Review checklist for a new module

- [ ] Every query that reads or writes an object filters on its owner from the authenticated caller (API1), with a cross-tenant test that gets 404.
- [ ] Every route group has the auth middleware of the one role that may call it, and a wrong-role test (API5).
- [ ] Request and response DTOs carry only the fields that caller may set and see (API3).
- [ ] Every list, array and page has a maximum, and the limits are documented (API4).
- [ ] Abusable flows are limited per account and idempotent (API6).
- [ ] No URL from a request is fetched (API7); outbound calls use `platform/httpclient`.
- [ ] Responses from other APIs are validated and their errors mapped (API10).
- [ ] No query, filter or path is built from raw strings (A03).
- [ ] No secret, token or personal data is logged; new secret settings redact themselves with `slog.LogValuer` (A09, API2).
- [ ] `make verify lint vuln` passes (A06).
