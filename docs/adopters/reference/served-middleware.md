# Served middleware reference

Every key kit reads under `services.<svc>.<block>`, what it defaults
to, which services read it, the order the middleware runs in, and
what a refused caller sees on each surface. It covers the kit-shipped
services — `api`, `rpc`, `mcp`, `socket` — and any service of yours
built on the same bridge.

Walkthroughs live in the guides:
[secure-remote-serving.md](../guides/secure-remote-serving.md) for
auth and limits end to end, and the "Enable auth and limits" step of
[REST](../guides/expose-cli-over-rest.md#9-enable-auth-and-limits),
[MCP](../guides/expose-cli-over-mcp.md#enable-auth-and-limits) and
[gRPC](../guides/expose-cli-over-grpc.md#10-enable-auth-and-limits).
The normative rules are in the
[serve-lifecycle contract, "Middleware"](../../contracts/serve-lifecycle.md#middleware);
where this page and the contract differ, the contract wins.

## Set a key

Put a block under the service it is for, or under `services.all` to
set it for every service:

```yaml
# ~/.config/mytool/config.yaml
services:
  all:                     # every service
    rate_limit:
      enabled: true
    timeouts:
      command: 2m
  api:
    body_limit:
      max_bytes: 4194304   # api only
    rate_limit:
      read:
        burst: 200         # api only; enabled still comes from all
```

The same keys come from the environment and from `-c`:

```bash
MYTOOL_SERVICES_ALL_RATE_LIMIT_ENABLED=true mytool serve
mytool serve -c services.api.body_limit.max_bytes=4194304
```

Each key resolves on its own, most specific first: the service's key
(flag, environment or file), then the `services.all` key, then the
tool's code option, then the kit default. A list replaces, never
merges. See
[Middleware configuration](../../contracts/serve-lifecycle.md#middleware-configuration).

Every mistake below is refused when `serve` starts, exit `2`, naming
the key, before anything binds:

- a key a block does not accept, or a value of the wrong type;
- anything under `services.all` that is not a middleware block
  (`addr`, `enabled`, the insecure opt-ins);
- a block set under a service that cannot apply it (the tables below
  say which).

## Planes and order

Middleware sits on one of two planes, and runs in a fixed order you
cannot change.

**HTTP plane** — every kit HTTP listener (the api service, the rpc
service, the mcp service over HTTP), outermost first:

| # | Middleware | Block | Refuses with |
|---|---|---|---|
| 1 | request id | — | — |
| 2 | client address | `trusted_proxies` | — |
| 3 | access log | — | — |
| 4 | recovery | — | — |
| 5 | tracing and metrics | `tracing`, `metrics` | — |
| 6 | security headers | `security_headers` | — |
| 7 | `/healthz`, `/readyz` | `health` | — (answers, ends the request) |
| 8 | Host and Origin checks, then the scrape endpoint | `host_check`, `origin_check`, `metrics.scrape` | `host_rejected`, `origin_rejected` |
| 9 | CORS | — ([not configurable yet](#not-configurable-yet)) | — |
| 10 | body limit | `body_limit` | `body_too_large` |
| 11 | compression | `compression` | — |
| 12 | authentication | `auth` | `unauthenticated` |
| 13 | your routes, then the command | — | the invocation plane below |

**Invocation plane** — every remote call, on every surface, once the
transport has decoded it:

| # | Gate | Block | Refuses with |
|---|---|---|---|
| 1–3 | resolve, enabled, invocable | — | `unknown_command`, `not_enabled`, `not_invocable` |
| 4 | a verified caller, for `kit/auth-required` | `auth` | `unauthenticated` |
| 5 | destructive ceiling | — (`Policy`) | `destructive_blocked` |
| 6 | permission: scopes, `--policy`, `cli.WithPermission` | — | `insufficient_scope`, `permission_denied` |
| 7 | rate limit | `rate_limit` | `rate_limited` |
| 8 | idempotency replay, then the result cache | `idempotency`, `cache` | `idempotency_conflict`, `idempotency_key_reused` |
| 9 | quota | `quota` | `quota_exceeded` |
| 10 | a person's confirmation | — | `confirmation_required` |
| 11 | capacity: in-flight slot or queue | `concurrency` | `overloaded` |
| 12 | run, under the command deadline | `timeouts` | `deadline_exceeded` |
| 13 | record: replay store, quota, audit | `idempotency`, `quota`, `audit` | never |

Why this order, and what each slot promises:
[Gate order](../../contracts/serve-lifecycle.md#gate-order-on-the-invocation-plane)
and
[HTTP plane order](../../contracts/serve-lifecycle.md#middleware-order-on-the-http-plane).
`tls` and the server `timeouts` configure the listener itself and
have no slot.

## Which services read each block

"HTTP" is the api service, the rpc service and the mcp service over
HTTP. "Bridge" is those three, the socket service, the mcp service
over stdio, and a service of yours built with `cli.ServeBridgeOptions`.

| Block | Read by | Refused under |
|---|---|---|
| `auth.mode`: `mtls`, `jwt`, `jwks`, `oidc`, `apikey` | HTTP | socket |
| `auth.mode`: `peer` | socket | api, rpc, mcp |
| `auth.peer` | socket | every other service |
| `auth.mtls`, `auth.jwt`, `auth.jwks`, `auth.oidc`, `auth.apikey`, `tls`, `tls.acme` | HTTP | socket |
| `timeouts` `read_header`, `read`, `write`, `idle` | HTTP | socket |
| `timeouts` `command` | bridge | — |
| `tracing`, `metrics` | HTTP requests; bridge invocations | — |
| `metrics.scrape`, `security_headers`, `health`, `host_check`, `origin_check`, `body_limit`, `compression`, `trusted_proxies` | HTTP | socket |
| `rate_limit`, `idempotency`, `quota`, `concurrency`, `audit`, `audit.redact` | bridge | — |
| `cache` | api | every other service |

Under `services.all` nothing is refused for reach: a service a block
does not apply to does not read it. The mcp service over stdio reads
no HTTP key.

"Loopback" below is a listener bound to `127.0.0.1`, `::1` or
`localhost`; the socket service and stdio count as loopback.

## Keys

### `auth`

| Key | Default | Meaning |
|---|---|---|
| `auth.mode` | unset | the verifier: `jwt`, `jwks`, `oidc` (bearer token), `apikey`, `mtls` (client certificate), `peer` (socket only). Unset: the code `Auth` of the service config, if any |
| `auth.jwt.public_key_files` | — | PEM public keys trusted beside the tool's identity key (`cli.WithIdentity`) |
| `auth.jwks.url` | — (required) | the JSON Web Key Set; `https`, or `http` to loopback |
| `auth.oidc.issuer` | — (required) | the provider; discovery yields its key set |
| `auth.<jwt\|jwks\|oidc>.issuer` | — | the token's `iss` must equal it |
| `auth.<jwt\|jwks\|oidc>.audience` | — (required under `jwks`, `oidc`) | the token's `aud` must include one; with an `issuer`, a URL audience also publishes protected resource metadata (RFC 9728) |
| `auth.<jwt\|jwks\|oidc>.clock_skew` | `1m` | tolerance on `exp`, `nbf`, `iat` |
| `auth.<jwks\|oidc>.refresh` | `1h` | how long a fetched key set is used |
| `auth.<jwt\|jwks\|oidc>.tenant_claim` | `tenant` | the claim the tenant is read from |
| `auth.apikey.backend` | `sqlite` | the key store: `sqlite` or `badger`; its driver must be imported |
| `auth.apikey.path` | `<data dir>/<tool>/apikeys.db` | the store's file or directory |
| `auth.peer.require_same_uid` | `false` | refuse a socket peer of another uid |
| `auth.peer.resolve_names` | `false` | name the peer by user name, not `uid:<n>` |
| `auth.peer.scopes` | unset | scopes every admitted socket peer holds |

A mode's keys under a service that names another mode are refused.
Rules: [Bearer tokens](../../contracts/serve-lifecycle.md#bearer-tokens),
[API keys](../../contracts/serve-lifecycle.md#api-keys),
[Socket peer credentials](../../contracts/serve-lifecycle.md#socket-peer-credentials).
The `mtls` keys are in the TLS table below.

### TLS and client certificates

| Key | Default | Meaning |
|---|---|---|
| `tls.enabled` | on when a certificate source is set | serve TLS only (HTTP/2 and HTTP/1.1); `false` leaves a listener plaintext |
| `tls.cert_file`, `tls.key_file` | — | PEM certificate chain and key, loaded at start |
| `tls.min_version` | `1.2` | `1.2` or `1.3` |
| `tls.acme.enabled` | on when `domains` is set | obtain and renew certificates by ACME (TLS-ALPN-01, on the listener) |
| `tls.acme.domains` | — | the names certificates are issued for |
| `tls.acme.cache_dir` | `<state dir>/<tool>/acme` | account key and certificates |
| `tls.acme.email` | — | contact the CA may use |
| `tls.acme.directory_url` | Let's Encrypt production | another ACME directory |
| `auth.mtls.ca_file` | — (required under `mtls`) | PEM bundle client certificates must chain to |
| `auth.mtls.principal` | `san` | `san` (URI, else DNS, else email SAN), `san_uri`, `san_dns`, `san_email`, `cn` |
| `auth.mtls.tenant_oid` | — | OID of a subject attribute or extension holding the tenant |
| `auth.mtls.tenant_san_pattern` | — | RE2 over the SANs; first capture group, else the whole match |

Rules: [TLS and client certificates](../../contracts/serve-lifecycle.md#tls-and-client-certificates).

### Limits

| Key | Default | Meaning |
|---|---|---|
| `body_limit.enabled` | `true` | cap request bodies |
| `body_limit.max_bytes` | 1 MiB; rpc 4 MiB | the cap, in bytes; `0` is the default |
| `rate_limit.enabled` | off on loopback, on beyond it | per-caller token buckets, one per side-effect tier |
| `rate_limit.read.per_minute`, `.burst` | `600`, `60` | commands declaring `kit/side-effect: read` |
| `rate_limit.write.per_minute`, `.burst` | `120`, `20` | write tiers, and commands that declare none |
| `rate_limit.destructive.per_minute`, `.burst` | `12`, `3` | destructive tiers |
| `quota.enabled` | on when `ops` or `bytes` is set | per-caller totals per window, kept across restarts |
| `quota.ops` | unset | calls per window |
| `quota.bytes` | unset | output bytes per window (stdout, stderr, data) |
| `quota.window` | `1h` | the window, aligned to the epoch (`24h` is a UTC day) |
| `quota.per` | `principal` | `principal` or `tenant` |
| `concurrency.enabled` | `true` | bound calls running at once, loopback included |
| `concurrency.max_inflight` | `32` | calls running at once |
| `concurrency.max_queue` | `64` | calls waiting for a slot; `0` refuses at once |
| `timeouts.read_header` | `5s` | reading a request's headers |
| `timeouts.read` | `5s` | reading a whole request |
| `timeouts.write` | `10s` | writing a response; stream responses exempt |
| `timeouts.idle` | the value of `read` | a keep-alive connection's wait for its next request |
| `timeouts.command` | none | deadline of a command with no `kit/timeout` annotation |

A caller, for the rate limit and the quota, is its verified principal
and tenant, else its client address, else the surface. Rules:
[Capacity](../../contracts/serve-lifecycle.md#capacity),
[Timeouts](../../contracts/serve-lifecycle.md#timeouts), slots 7 and 9
of the [gate order](../../contracts/serve-lifecycle.md#gate-order-on-the-invocation-plane).

### Browser and proxy hardening

| Key | Default | Meaning |
|---|---|---|
| `host_check.enabled` | `true` | refuse a `Host` the listener does not answer for |
| `host_check.allow` | `[]` | extra hosts, `name` or `name:port`; a wildcard bind checks only these |
| `origin_check.enabled` | `true` | refuse cross-origin browser writes |
| `origin_check.allow` | `[]` | origins allowed to write, `scheme://host[:port]` |
| `security_headers.enabled` | `true` | `nosniff`, `no-referrer`, a deny-all CSP; HSTS over TLS |
| `trusted_proxies` | `[]` | CIDRs and addresses whose forwarding headers are believed; a list, not a block |
| `health.enabled` | `true` | serve `/healthz` and `/readyz` |
| `health.path_prefix` | `""` | mount both probes under a path prefix |
| `health.detail` | `true` on loopback, else `false` | name failing checks in a `/readyz` `503` |
| `compression.enabled` | `false` | gzip or zstd response bodies |
| `compression.min_bytes` | `1024` | smallest body compressed |

Rules: [Client address](../../contracts/serve-lifecycle.md#client-address),
[Readiness over HTTP](../../contracts/serve-lifecycle.md#readiness-over-http).

### Replay, cache and audit

| Key | Default | Meaning |
|---|---|---|
| `idempotency.enabled` | `true` | replay a keyed call the same caller already completed |
| `idempotency.ttl` | `24h` | how long a record replays |
| `cache.enabled` | `true` | answer reads that declare `kit/cache-ttl` from a cache (api only) |
| `cache.backend` | `memory` | `memory`, or `sqlite` / `badger` with their driver imported |
| `cache.max_bytes` | 64 MiB | memory backend only |
| `cache.path` | — (required for a file backend) | file or directory of the store |
| `audit.sinks` | none | sinks from configuration; `chain` is a tamper-evident log |
| `audit.redact.secret_flags` | none | extra flag names masked |
| `audit.redact.patterns` | none | extra RE2 patterns masked |
| `audit.redact.max_field_bytes` | `4096` | longest field the patterns scan; longer is withheld |

An `audit.sinks` entry is `chain`, or a map of `type`, `path`,
`fsync`, `max_bytes`, `max_files`, `on`, `surfaces`, `paths`; see
[Keep a tamper-evident trail](../guides/secure-remote-serving.md#keep-a-tamper-evident-trail).
Redaction of declared secret flags cannot be switched off. Rules:
[Idempotency](../../contracts/serve-lifecycle.md#idempotency),
[result cache](transport-api.md#result-cache),
[Audit](../../contracts/serve-lifecycle.md#audit).

### Tracing and metrics

| Key | Default | Meaning |
|---|---|---|
| `tracing.enabled`, `metrics.enabled` | `false` | export spans, metrics |
| `tracing.exporter`, `metrics.exporter` | `otlp` | `otlp` or `stdout`; metrics also `none` (scrape only) |
| `tracing.endpoint`, `metrics.endpoint` | `OTEL_EXPORTER_OTLP_*`, else `http://127.0.0.1:4318` | OTLP collector |
| `tracing.headers`, `metrics.headers` | none | headers on every export |
| `tracing.sample_ratio` | `1` | fraction of new traces recorded |
| `metrics.interval` | `60s` | export period |
| `metrics.scrape.enabled` | `false` | answer a Prometheus scrape; needs `metrics.enabled` |
| `metrics.scrape.path` | `/metrics` | where the scrape answers |
| `metrics.scrape.allow_remote` | `false` | allow the scrape on a non-loopback bind |

Nothing exports until the tool links a provider
(`cli.WithObservability`) and a key enables it. Names of spans and
instruments: [served-observability.md](served-observability.md).

### Not configurable yet

- **CORS (HTTP slot 9).** No kit-shipped listener answers CORS, and
  no block configures it. A browser app on another origin reaches the
  service through a proxy that answers CORS, or through your own
  router built with `api.CORS`.

## Refusals by surface

One code per refusal class, the same string on every surface:

| Code | REST (api) | Connect (rpc) | MCP | Socket | Exit class |
|---|---|---|---|---|---|
| `unauthenticated` | `401` + `WWW-Authenticate` | `Unauthenticated` | HTTP edge: `401`; gate 4: `isError` | `UNAUTHENTICATED` | 5 |
| `insufficient_scope` | `403` + `WWW-Authenticate: Bearer error="insufficient_scope", scope="…"` | `PermissionDenied` | bearer token over HTTP: `403` + the same challenge; otherwise `isError` | `DENIED` | 5 |
| `rate_limited` | `429` + `Retry-After` | `ResourceExhausted` | `isError` | `RATE_LIMITED` | 64 |
| `quota_exceeded` | `429` + `Retry-After` (window reset) | `ResourceExhausted` | `isError` | `QUOTA_EXCEEDED` | 64 |
| `overloaded` | `503` + `Retry-After` | `Unavailable` | `isError` | `OVERLOADED` | 6 |
| `deadline_exceeded` | `504` | `DeadlineExceeded` | `isError` | `DEADLINE_EXCEEDED` | 6 |
| `idempotency_conflict` | `409` | `Aborted` | `isError` | `CONFLICT` | 4 |
| `idempotency_key_reused` | `422` | `InvalidArgument` | `isError` | `CONFLICT` | 2 |
| `body_too_large` | `413` | `ResourceExhausted` | `413`, JSON-RPC `-32600` | — (1 MiB line bound ends the connection) | 2 |
| `host_rejected` | `403` | `PermissionDenied` | `403` | — | 5 |
| `origin_rejected` | `403` | `PermissionDenied` | `403` | — | 5 |

How each surface carries it:

- **REST.** The body is `{"status", "code", "message"}` with `code`
  the class code. Behind a protected resource (a bearer mode with an
  issuer and a URL audience), the `401` and `403 insufficient_scope`
  challenges also carry
  `resource_metadata="<origin>/.well-known/oauth-protected-resource<path>"`.
- **Connect.** The Connect code is the class; an HTTP-plane refusal
  (Host, Origin, body limit) also leads its message with the class
  code. `rate_limited`, `quota_exceeded` and `overloaded` carry
  `Retry-After` in the error metadata. Connect's HTTP mapping gives
  the REST status for every class but `idempotency_key_reused`
  (`400`) and `body_too_large` (`429`).
- **MCP.** A refusal on the HTTP plane is an HTTP status with a
  JSON-RPC error body (`id` null when unknown). A refusal on the
  invocation plane is a `tools/call` result with `isError: true`, its
  text leading with the code, and `_meta["hop.top/refusal"]` of
  `{"code", "retry_after_ms"}`. The one exception is
  `insufficient_scope` for a caller that presented a bearer token over
  HTTP: `403` with the challenge, so the client can step up its token.
- **Socket.** `error.code` is the socket code; a retryable class
  carries `retry_after_ms`.
- **Exit class** is the code a client reports when it turns a refusal
  into a process exit: 2 `USAGE`, 4 `CONFLICT`, 5 `UNAUTHORIZED`, 6
  `TRANSIENT`, 64 `RATE_LIMITED`.

Every invocation-plane refusal is audited. HTTP-plane refusals are
counted by code in `kit.serve.http.refusals` and logged. Mapping
rules: [Refusals](../../contracts/serve-lifecycle.md#refusals). The
command-level refusals (`unknown_command`, `not_invocable`,
`destructive_blocked`, `permission_denied`, `confirmation_required`)
are mapped per surface in
[cmdsurface.md](cmdsurface.md).

## Code options

What only code can say, or the tool's own default below
configuration:

| Option | Sets |
|---|---|
| `APIConfig.Auth`, `rpcserve.Config.Auth`, `mcpserve.Config.Auth`, `SocketConfig.Auth` | a verifier of your own; `auth.mode` replaces it when set |
| `APIConfig.MaxBodyBytes`, `rpcserve.Config.MaxBodyBytes` | the body cap below `body_limit.max_bytes` |
| `cli.WithServeRateLimit(cmdsurface.RateLimit{...})` | tier numbers below `rate_limit.*`; does not switch the limit on |
| `SocketConfig.PeerScopes` | scopes of a peer when `auth.peer.scopes` is unset |
| `cli.WithTokenCheck(fn)` | a revocation check on every credential an `auth.mode` verifier accepted |
| `cli.WithIdentity`, `cli.WithAPIKeys` | the keypair `jwt` trusts and `token create` signs with; the `token key` verbs |
| `cli.WithAuditSinks`, `cli.WithAuditCommand` | sinks beside `audit.sinks`; `audit verify` |
| `cli.WithObservability(p)` | the tracing and metrics provider |
| `cli.WithServeIdempotencyStore`, `cli.WithUsageStore` | the replay store; the quota and budget store |
| `kit/timeout`, `kit/cache-ttl` annotations | one command's deadline; one read's cache lifetime |

## Related pages

- [secure-remote-serving.md](../guides/secure-remote-serving.md) —
  auth, policy, audit and limits, step by step
- [served-observability.md](served-observability.md) — spans,
  instruments and scrape
- [transport-api.md](transport-api.md) — the api package: auth
  shapes, guards, health, compression, the result cache
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md#middleware)
  — the normative rules
