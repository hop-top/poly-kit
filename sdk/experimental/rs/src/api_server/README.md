# api_server

## What it answers

How a kit tool serves HTTP: the adopter's axum routes inside the middleware stack every kit listener shares, run as a service the supervisor starts, probes and stops. The command-tree projection (`commands`, features `api-server` + `cli`) sits on this listener; RPC transports are not here.

## Use it when

- a tool needs a REST listener under `serve` → `ApiService::build(ApiServerConfig::new(addr, router))`, register it, `serve` runs it
- callers authenticate → set `auth` to an `authn` `AuthFunc` (`Verifier::auth_func`, `ApiKeys::auth_func`)
- it sits behind a proxy → `trusted_proxies`; only then are `Forwarded` and `X-Forwarded-*` believed
- clients are browsers on another origin → `cors`, and the same origins are trusted by the Origin check
- ops needs probes and a spec → `/healthz`, `/readyz` come with the stack; `openapi` serves `/openapi.json` from an `OpenApi` model
- a handler streams → `sse(stream)`
- a kit CLI must be callable over HTTP → `commands::CommandProjection::new(Arc::new(root), name, version)`; merge `.router()` into the routes, and `.openapi(..)` into the spec

## Quick start

```rust
use axum::routing::get;
use hop_top_kit::api_server::{ApiServerConfig, ApiService};

let routes = axum::Router::new().route("/ping", get(|| async { "pong" }));
let svc = ApiService::build(ApiServerConfig::new("127.0.0.1:8080", routes))?;
// Register `Arc::new(svc)` in a serve::ServiceRegistry.
```

## Command projection

`commands` projects the tree a kit CLI root defines (`cli::Root::model`), as Go's `go/transport/api` projection does for the Go root: `GET /v1/commands` lists every command (invocable first, withheld ones with their reason); a `read` command is `GET /v1/commands/<path>` with flags as query parameters, any other tier `POST` with `{"flags":{},"args":[]}`; `<route>/stream` answers as `text/event-stream` (`event` frames, then one `result`). The command's exit class selects the status (`status_for_exit_code`: USAGE 400, NOT_FOUND 404, CONFLICT 409, UNAUTHORIZED and CONSENT_REFUSED 403, TRANSIENT and PREREQUISITE 503, RATE_LIMITED 429, PROVENANCE_MISSING 422, else 500). Only declared flags are accepted, so persistent plumbing (`--dry-run`, `-C`) is never reachable. A command with no handler, hidden, interactive or in a management group is listed and not mounted. `with_gate` is the per-command policy hook; authentication, body limit and rate limit are this listener's. Not ported: `--confirm` gating, idempotency replay, result caching, personalised listings. Pinned by the `projection` table of `contracts/parity/api.json`, run by `go/transport/api/projection_contract_test.go` and `tests/api_server_commands.rs`.

## Contract

- Go is canonical. Client address, trusted-proxy parsing, Host and Origin decisions, listener hosts, the constants and the OpenAPI documents are pinned by [`contracts/parity/api.json`](../../../../../contracts/parity/api.json), run by both ports (`make test-parity-api`).
- Order, outer to inner: request id, client address, access log, recovery, security headers; health routes; Host check, Origin check; CORS, body limit, gzip, rate limit, auth; the request events on each matched route.
- Defaults are the safe ones: Host and Origin checks, security headers, 1 MiB body cap (0 is the default, negative none) and loopback bind. A non-loopback bind without `auth` is refused by `validate` unless `insecure_remote`; a wildcard bind checks no Host unless `allow_hosts` names some.
- Every refusal is `{"status","code","message"}` JSON: `host_rejected`, `origin_rejected`, `body_too_large`, `unauthenticated` (401, `WWW-Authenticate: Bearer`), `rate_limited` (429, `Retry-After`), `method_not_allowed`. A panic is a plain 500.
- `/healthz` is liveness. `/readyz` is 200 only while the listener is up and every `readiness` check passes; failing names show on a loopback bind or with `health_detail`. They answer before Host, Origin, limits and auth, and are not overridable by a route of the same path.
- The access log line passes through the shared `redact` policy whole. `Authorization`, `X-API-Key` and `Cookie` are marked sensitive.
- Rate limit is per client address (the forwarded client behind a trusted proxy; IPv6 by /64), on `breaker`'s bursty limiter. A live bucket is never evicted (that would refill its owner): at `max_clients`, buckets idle for a full period are reclaimed by a sweep that runs at most once per quarter period; newcomers past the cap share one bucket per /24 (IPv6 /48), at most cap/4 of them, then a single overflow bucket, so under that much key pressure the degradation falls on unseen clients and existing ones keep their budgets. At most `per_prefix` (16) individual buckets per /24 or /48, so one prefix cannot take every slot; its extra clients use the prefix's aggregate. A request with no parseable client address has a bucket of its own. `Retry-After` is the period, an upper bound. Go has no per-client limiter in `api` (its limit is in the invocation plane), so there is nothing to diverge from.
- Residual risk: the limiter bounds memory and keeps per-client fairness inside one process. A distributed attack across many prefixes degrades to the shared overflow bucket by design; volumetric DDoS belongs at the edge (proxy or CDN).
- An incoming `X-Request-ID` outside `[A-Za-z0-9._-]{1,128}` is replaced with a fresh id; the id is quoted in the log line. Go's `RequestID` does the same; `request_id` in `contracts/parity/api.json` pins both.
- Events: `kit.api.request.started` and `.ended` (or `<prefix>.started`/`.ended`, a 3-segment prefix) with source `api.router`, only for matched routes; `ended` carries status and `duration`.
- `OpenApi` emits 3.1.0 documents with schemas verbatim; `crud` describes a resource's five operations. No `$schema` link properties.
- Gzip only, 1 KiB and up, for text-like types; event streams, upgrades, Connect and gRPC are never compressed.
- Feature `webhook` adds `api_server::webhook`, the inbound webhook receiver (`Receiver::builder(name)`, presets `graph`, `pipedrive`, `generic_hmac`; mount `receiver.route()`): HMAC, clientState and basic-auth verifiers, Graph validationToken handshake, replay window, an atomic put-if-absent claim in a `kv::TtlInserter` (released if the publish fails), 202 within the ack budget, then the bus. Same behavior as `go/transport/webhook`, pinned by [`contracts/webhook-v1`](../../../../../contracts/webhook-v1/README.md); sha1 senders (legacy `X-Hub-Signature`) only under the explicit `webhook-sha1` feature, never by default.
- Not ported: websocket hub, the command-tree projection and `/v1/commands`, mTLS and TLS serving, read and write timeouts, idempotency keys, the metrics endpoint.

## Neighbours

- `serve`: the `Service` this implements; `authn`: the `AuthFunc`; `breaker`: the limiter; `redact`: the log policy; `bus`: the event sink
- `go/transport/api`: the Go implementation; `contracts/parity/api.json`: the corpus
