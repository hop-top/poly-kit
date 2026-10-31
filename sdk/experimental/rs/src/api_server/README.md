# api_server

## What it answers

How a kit tool serves HTTP: the adopter's axum routes inside the middleware stack every kit listener shares, run as a service the supervisor starts, probes and stops. Wrong place for the command-tree projection and RPC transports: those sit on top of this listener, and are not ported here.

## Use it when

- a tool needs a REST listener under `serve` → `ApiService::build(ApiServerConfig::new(addr, router))`, register it, `serve` runs it
- callers authenticate → set `auth` to an `authn` `AuthFunc` (`Verifier::auth_func`, `ApiKeys::auth_func`)
- it sits behind a proxy → `trusted_proxies`; only then are `Forwarded` and `X-Forwarded-*` believed
- clients are browsers on another origin → `cors`, and the same origins are trusted by the Origin check
- ops needs probes and a spec → `/healthz`, `/readyz` come with the stack; `openapi` serves `/openapi.json` from an `OpenApi` model
- a handler streams → `sse(stream)`

## Quick start

```rust
use axum::routing::get;
use hop_top_kit::api_server::{ApiServerConfig, ApiService};

let routes = axum::Router::new().route("/ping", get(|| async { "pong" }));
let svc = ApiService::build(ApiServerConfig::new("127.0.0.1:8080", routes))?;
// Register `Arc::new(svc)` in a serve::ServiceRegistry.
```

## Contract

- Go is canonical. Client address, trusted-proxy parsing, Host and Origin decisions, listener hosts, the constants and the OpenAPI documents are pinned by [`contracts/parity/api.json`](../../../../../contracts/parity/api.json), run by both ports (`make test-parity-api`).
- Order, outer to inner: request id, client address, access log, recovery, security headers; health routes; Host check, Origin check; CORS, body limit, gzip, rate limit, auth; the request events on each matched route.
- Defaults are the safe ones: Host and Origin checks, security headers, 1 MiB body cap (0 is the default, negative none) and loopback bind. A non-loopback bind without `auth` is refused by `validate` unless `insecure_remote`; a wildcard bind checks no Host unless `allow_hosts` names some.
- Every refusal is `{"status","code","message"}` JSON: `host_rejected`, `origin_rejected`, `body_too_large`, `unauthenticated` (401, `WWW-Authenticate: Bearer`), `rate_limited` (429, `Retry-After`), `method_not_allowed`. A panic is a plain 500.
- `/healthz` is liveness. `/readyz` is 200 only while the listener is up and every `readiness` check passes; failing names show on a loopback bind or with `health_detail`. They answer before Host, Origin, limits and auth, and are not overridable by a route of the same path.
- The access log line passes through the shared `redact` policy whole. `Authorization`, `X-API-Key` and `Cookie` are marked sensitive.
- Rate limit is per client address (the forwarded client behind a trusted proxy), on `breaker`'s bursty limiter; a full table shares one bucket. `Retry-After` is the period, an upper bound.
- Events: `kit.api.request.started` and `.ended` (or `<prefix>.started`/`.ended`, a 3-segment prefix) with source `api.router`, only for matched routes; `ended` carries status and `duration`.
- `OpenApi` emits 3.1.0 documents with schemas verbatim; `crud` describes a resource's five operations. No `$schema` link properties.
- Gzip only, 1 KiB and up, for text-like types; event streams, upgrades, Connect and gRPC are never compressed.
- Not ported: websocket hub, the command-tree projection and `/v1/commands`, mTLS and TLS serving, read and write timeouts, idempotency keys, the metrics endpoint.

## Neighbours

- `serve`: the `Service` this implements; `authn`: the `AuthFunc`; `breaker`: the limiter; `redact`: the log policy; `bus`: the event sink
- `go/transport/api`: the Go implementation; `contracts/parity/api.json`: the corpus
