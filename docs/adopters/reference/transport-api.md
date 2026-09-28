# api package reference

Wire-level reference for
[`go/transport/api`](../../../go/transport/api/README.md): bus
integration topics, the REST command projection (route shape,
parameters, discovery, response body, exit-code mapping, refusals),
auth claims, request provenance, transport guards, the OpenAPI
document, response compression, and the read result cache. The task
walkthrough is
[expose-cli-over-rest.md](../guides/expose-cli-over-rest.md).

## Bus integration

The bus-integration middleware publishes one event at request start
and one at request end (after the handler returns).

### Default topics

| Topic                          | When |
|--------------------------------|------|
| `kit.api.request.started`      | before handler runs |
| `kit.api.request.ended`        | after handler returns |

Earlier releases emitted `api.request.start` and `api.request.end`,
both non-conformant (3 segments, present-tense). The old topics have
been removed with no back-compat alias. Subscribers MUST update.

### Adopter rebrand

```go
import "hop.top/kit/go/transport/api"

r := api.NewRouter(
    api.WithBusIntegration(b,
        api.WithTopicPrefix("myapp.api.request"),
    ),
)
// emits: myapp.api.request.{started,ended}
```

`WithTopics` overrides individual topics; non-empty entries are
validated via `bus.ValidateTopic` (panics on invalid input).

## Command projection

A tool that registers the `api` service gets a REST projection of its
own command tree for free. No `Expose`, no `MountREST`, no mounting
code: the service reflects the completed cobra tree when it starts and
mounts one route per conformant command, plus an OpenAPI document
describing them.

Reflection happens at **service start**, not at registration, because
that is the first moment the tree is complete.

This is additive. `APIConfig.Handlers` and `APIConfig.Resources` keep
working unchanged, and they are mounted **first**, so an adopter route
always wins a pattern collision.

A bare cobra tree — no kit root — gets the same projection from its
bridge with `cmdsurface.MountProjection(b, r, opts...)`; see
[cmdsurface.md](cmdsurface.md#rest). Everything below applies to both.

### Route shape

Everything lives under a versioned prefix:

```text
/v1/commands/<command>/<subcommand>
/v1/commands/<command>/<subcommand>/stream
```

The second is the command's [streaming](#streaming) twin.

The version is in the path rather than a header because the shape of
the projection is derived from the command tree: adding a required
flag changes a request schema without the adopter touching a route.
A path version gives that churn somewhere to land.

The older `cmdsurface.MountREST` mount (default prefix `/cmd`,
POST-with-`Invocation`-envelope) is deprecated and frozen; the
projection replaces it, and `MountRPC` carries a call envelope.

### Method selection

| Side-effect class | Method | Rationale |
|-------------------|--------|-----------|
| `read`            | `GET`  | safe and cacheable |
| `write`           | `POST` | not safe, not cacheable |
| `destructive`     | `POST` | as above; also gated by policy |
| unannotated       | `POST` | no `kit/side-effect`: kit cannot claim the call is safe, so it is never `GET` |
| `interactive`     | —      | never mounted |

The class comes from `kit/side-effect`. A command that declares none is
projected as `write`, and discovery marks it `side_effect_source:
"unannotated"`; see
[how `kit/side-effect` resolves](cmdreflect.md#how-kitside-effect-resolves).

Only two methods. A finer mapping (`PUT` for idempotent writes,
`DELETE` for destructive ones) reads better in isolation but cannot be
honored: kit's vocabulary has no notion of resource identity, so there
is no target for `PUT`/`DELETE` semantics, and a caller seeing
`DELETE` would reasonably expect the URL to name the thing deleted.

### Parameters

| Method | Flags | Positional arguments |
|--------|-------|----------------------|
| `GET`  | query string, typed per the flag's declared type | repeated `?arg=` in order |
| `POST` | `flags` object in a JSON body | `args` array in the same body |

A `POST` also honors query flags so the two can be mixed; the body
wins on conflict, being the more explicit statement. Undeclared query
parameters are ignored (query strings collect tracking junk); an
undeclared flag in a **body** is a `400` naming the flag.

Hidden and deprecated flags are not projected: they are not part of
the supported surface.

### Discovery

`GET /v1/commands` lists **every** reflected command, mounted or not.
Non-invocable entries carry `invocable: false` and a stable reason;
they carry no `method`, `route` or `stream_route`, since advertising
a route that can only 404 helps nobody. `stream_route` is served on
`method`; see [Streaming](#streaming).

```json
{
  "prefix": "/v1/commands",
  "commands": [
    {"name": "list", "side_effect": "read", "side_effect_source": "declared",
     "invocable": true, "method": "GET", "route": "/v1/commands/list",
     "stream_route": "/v1/commands/list/stream"},
    {"name": "sync", "side_effect": "write", "side_effect_source": "unannotated",
     "invocable": true, "method": "POST", "route": "/v1/commands/sync",
     "stream_route": "/v1/commands/sync/stream"},
    {"name": "shell", "side_effect": "interactive", "side_effect_source": "declared",
     "invocable": false, "reason": "interactive"}
  ],
  "reasons": ["interactive"],
  "exit_status": [{"exit_code": 0, "status": 200}]
}
```

Invocable commands sort first. The reason vocabulary is the reflector's
(`interactive`, `unauthorized-destructive`, `hidden-internal`,
`deprecated`, `not-runnable`, `builtin`, `management-only`,
`malformed-schema`) and appears in the OpenAPI document as an enum.

`side_effect_source` is on every entry and says whether `side_effect`
is the adopter's word: `declared`, or kit's conservative stand-in —
`inferred` (the destructive-name heuristic), `unannotated` (no
annotation; projected as `write`), `malformed` (an annotation kit could
not resolve). Treat anything but `declared` as a guess.

### Policy

Execution goes through the `cmdsurface` `Bridge`, so safety level,
permissions and confirmation are enforced by the same gate every other
surface uses. Interactive and unauthorized-destructive commands are
withheld at mount, not refused per call.

Permitting destructive commands (`Policy.AllowDestructiveOn`) lifts
the transport ceiling only. A command declaring
`kit/requires-confirmation` still runs its own gate, which has no TTY
here, so the call must carry `"flags":{"confirm":"yes"}`; a typed-token
command additionally needs `confirm-token`, and the refusal names the
expected value. The snippets are in the guide:
[permit a destructive command](../guides/expose-cli-over-rest.md#6-permit-a-destructive-command).

`Hide` patterns are `"widget add"`, `"widget *"`, or `"*"`. Hidden
commands stay in discovery with `invocable: false` and the reason
`withheld-by-config`, and other surfaces are unaffected. `Expose` is
the allow-list counterpart: empty mounts the whole tree, and `Hide`
is applied after it. Snippet:
[keep a command off REST](../guides/expose-cli-over-rest.md#7-keep-a-command-off-rest).

### Response body

A command that runs answers with one object:

```json
{"exit_code": 0, "data": {"widgets": [{"id": "w-1"}]}}
```

| Field | When present |
|---|---|
| `exit_code` | always |
| `data` | the command declares an output schema (`cli.SetOutputSchema`); it is the command's output decoded from its own `--format=json` rendering |
| `stdout` | the command declares no schema, or wrote something other than one JSON document; carries the command's default rendering |
| `stderr` | the command wrote to standard error, or failed without writing — then the error's message |

`format` is a root flag rather than one the command declares, so the
projection does not accept it: a schema-declaring command always
answers in `data`, and every other command in its default rendering.
The full rule is in the
[execution contract](../../contracts/serve-lifecycle.md#format-selection-and-structured-output).

A request's context is the command's: a client that disconnects
cancels the command it started. Commands run one at a time per
service by default — the in-process runner serializes on the shared
command tree — or in parallel, each on a tree of its own, when the
tool opts in with `cli.WithRootFactory`. Either way each starts from
the flag state the operator's own command line left, plus only the
flags the request carries.

### Exit codes

A command that runs returns its structured output as the response
body, with its exit code mapped to a status:

| Exit | Code | Status | Meaning |
|------|------|--------|---------|
| 0  | `OK`                 | 200 | success |
| 1  | `GENERIC`            | 500 | unclassified failure |
| 2  | `USAGE`              | 400 | the request was wrong: a usage error the command raised, or a positional or flag error its parser raised |
| 3  | `NOT_FOUND`          | 404 | |
| 4  | `CONFLICT`           | 409 | |
| 5  | `UNAUTHORIZED`       | 403 | see below |
| 6  | `TRANSIENT`          | 503 | retry may clear it |
| 7  | `CONSENT_REFUSED`    | 403 | re-send the same call with confirmation |
| 64 | `RATE_LIMITED`       | 429 | |
| 65 | `PROVENANCE_MISSING` | 422 | well-formed, cannot be acted on |
| 70 | `PREREQUISITE`       | 503 | a declared dependency is unreachable; repair it and retry the identical call |

Any other exit code is 500.

`UNAUTHORIZED` maps to **403, not 401**. Auth already ran and passed
before the command executed, so the refusal is about what this
authenticated caller may do — which is 403's meaning. A 401 would
invite a pointless retry with credentials.

Refusals where the command never ran are distinct from exit codes:

| Condition | Status | Code |
|-----------|--------|------|
| command withheld on this surface | 404 | `not_invocable` |
| policy refuses a destructive command | 403 | `destructive_blocked` |
| the caller's credential lacks a scope the command's `kit/permissions` names | 403 + `WWW-Authenticate: Bearer error="insufficient_scope", scope="…"` | `insufficient_scope` |
| the permission gate refuses this caller | 403 | `permission_denied` |
| the `Idempotency-Key` names a call still running | 409 | `idempotency_conflict` |
| the `Idempotency-Key` was used for a different call | 422 | `idempotency_key_reused` |

A call that repeats one its caller already completed under the same
`Idempotency-Key` is not a refusal: it answers with the first call's
status and body, and `Idempotent-Replayed: true` (`CommandResult.Replayed`
on the executor side). Rules:
[contract, Idempotency](../../contracts/serve-lifecycle.md#idempotency).

A `not_invocable` body carries the descriptor's reason, which is what
separates "no such command" from "that command exists and is withheld".
A `permission_denied` body carries the gate's stable reason; it is
`403`, not `401`, because the caller is authenticated and the refusal
is about what this caller may do. It is distinct from
`destructive_blocked` because different people fix them: the ceiling
is the deployment's policy, the denial is the caller's entitlement.

An unconfirmed destructive command is refused by the command itself,
not by the projection. A served request has no terminal to ask at, so
the confirmation gate takes its non-TTY default and declines; the
table above maps that to `403`, with the command's own message — which
names the `confirm` flag to re-send — in `stderr`. Pass
`{"flags":{"confirm":"yes"}}` to clear it.

`CONSENT_REFUSED` and `UNAUTHORIZED` share `403` because both are the
server declining a call it understood. They differ in who clears them
and how: a consent refusal is cleared by the same caller re-sending
the same command with confirmation, an authorization refusal by
someone else granting the caller permission. Read the envelope's
`code`, not the status, to tell them apart.

`PREREQUISITE` is `503` rather than `500`: the request was correct and
the command's own logic never ran, because a dependency it declared
was unreachable. An operator repairs the dependency and the identical
call succeeds, which is what `503` tells a client — unlike `TRANSIENT`,
also `503`, a prerequisite failure will not clear on its own, so a
backoff loop against it burns its budget. Branch on the envelope's
`code` when that distinction matters.

### Streaming

Every projected command has a streaming twin one segment down:

```text
/v1/commands/<command>/<subcommand>/stream
```

It is served on the **same method** as the command's own route and
takes the **same parameters**: a read streams on `GET` with its flags
in the query string, which is what a browser `EventSource` can open;
a write or a destructive command streams on `POST` with the JSON
body, so streaming never turns a call that changes state into a
`GET`. The segment cannot collide with a command, because only leaves
are projected and a leaf has no children.

```sh
curl -N 'http://127.0.0.1:8080/v1/commands/item/watch/stream?count=3'
```

```text
event: event
data: {"kind":"stdout","data":"tick 1: 2 items","at":"2026-09-27T12:00:00Z"}

event: event
data: {"kind":"stdout","data":"tick 2: 2 items","at":"2026-09-27T12:00:01Z"}

event: event
data: {"kind":"stdout","data":"tick 3: 2 items","at":"2026-09-27T12:00:02Z"}

event: result
data: {"status":200,"exit_code":0,"stdout":"tick 1: 2 items\ntick 2: 2 items\ntick 3: 2 items\n"}
```

The response is `text/event-stream`, in the frame vocabulary the
`cmdsurface` SSE surface uses:

| Frame | Data | When |
|---|---|---|
| `event` | `{"kind", "data", "at"}`: one line of `stdout` or `stderr` as the command writes it, or a `progress` payload | while the command runs |
| `result` | the [response body](#response-body) plus `status`, the status the request/reply route would have answered for this exit code | once, last, when the command ran to an exit code |
| `error` | an `APIError` (`status`, `code`, `message`), mapped as the request/reply route maps it; `503` `shutting_down` when the server is stopping | once, last, when the run failed without an exit code, or the server stopped it |
| `: ping` | comment | every 15 seconds while the command is silent, so proxies keep the connection open |

**Refusals come before the stream.** Everything the transport or the
bridge refuses is answered with an ordinary status and JSON body,
exactly as on the request/reply route, and nothing runs: a malformed
request (`400`), a failed authentication (`401`), a withheld command
(`404` — the stream route is not mounted either), the destructive
ceiling and the permission gate (`403`). Every refusal is audited as
it is on the request/reply route.

**Outcomes come in the terminal frame.** Once admitted the stream
opens at once, and the command's outcome — success, failure, or its
own confirmation refusal — is the `result` frame. The confirmation
gate belongs to the command, not the projection: its verdict is an
exit code, which exists only once the command has run. The frame's
`status` is `403` there, the same answer the request/reply route
gives, so a client classifies both routes' outcomes with one table.
Pass `confirm` in the body to clear it, as on the request/reply route.

**A client that disconnects cancels the command.** The request's
context is the command's; closing the connection cancels it, and the
run is audited as a cancellation. The server's write deadline, sized
for request/reply, is lifted for stream responses only.

**Stopping the service ends open streams.** A draining server waits
for in-flight requests, and a stream has no end of its own, so the
`api` service cancels every streaming command when it begins to stop
and ends each stream with an `error` frame, `503` `shutting_down`.
Request/reply calls drain as before.

**One tree, one command at a time.** Unless the tool opts in with
`cli.WithRootFactory`, commands share the tool's command tree and run
one at a time — a stream that runs for an hour holds the tree for an
hour. Serve long-running commands with a root factory.

Discovery advertises each invocable command's `stream_route`, and the
OpenAPI document describes each as its own operation
(`stream_commands_<path>`, `200` as `text/event-stream`).

#### Long-running work on each transport

| Transport | Long-running call | Client holds | Survives a disconnect |
|---|---|---|---|
| REST (`api` service) | `/v1/commands/<path>/stream`, server-sent events | the HTTP response | no: disconnecting cancels the command |
| RPC (`rpc` service, `cmdsurface.MountRPC`) | `InvokeStream`, server-streaming | the RPC stream | no: canceling the stream cancels the command |
| MCP (`mcpsdk.WithTasks`, experimental) | a task: `tools/call` returns a task id, then `tasks/get` / `tasks/cancel` | nothing between polls | yes: the task outlives the request |

REST and RPC stream a command's output while a client holds the
connection. MCP's tasks extension is the one shape for work that
must outlive its caller; it is experimental and follows a draft
specification. See [mcpsdk.md](mcpsdk.md#tasks-extension-sep-2663-experimental)
and the [cmdsurface reference](cmdsurface.md#rpc).

### Auth

The projection installs no auth. Routes are registered through the
router, so `APIConfig.Auth` and the rest of the middleware stack wrap
them exactly as they wrap an adopter's own routes — discovery
included.

The api service listens on `127.0.0.1:8080` by default and refuses a
non-loopback address it would serve unauthenticated, at exit `2`,
unless `services.api.insecure_remote` opts in. `Auth` is what makes
any other address acceptable.

`Auth(fn)` refuses a request `fn` rejects with `401`, code
`unauthenticated` (`CodeUnauthenticated`), `fn`'s error as the
message, and `WWW-Authenticate: Bearer`; `AuthChallenge(s)` names
another challenge. The refusal is recorded with `RecordRefusal`, so
the HTTP refusal metrics count it. A request `fn` accepts carries its
claims and the verified mark `Authenticated(ctx)`; a command declaring
`kit/auth-required` runs only for such a request, loopback included,
and is answered with the same `401` otherwise. The rules are normative in the
[serve lifecycle contract](../../contracts/serve-lifecycle.md#security);
the walkthrough is
[secure-remote-serving.md](../guides/secure-remote-serving.md).

#### Body limit

`BodyLimit(maxBytes, opts...)` caps the request body: a declared
`Content-Length` over the cap is refused with `413` before the handler
runs, and any other body is wrapped in `http.MaxBytesReader`, so a
chunked body fails the read that crosses the cap. `0` means
`DefaultMaxBodyBytes` (1 MiB); negative disables. The refusal body is
the usual `APIError` with code `body_too_large`
(`CodeBodyTooLarge`):

```json
{"status":413,"code":"body_too_large","message":"request body exceeds 1048576 bytes"}
```

A handler that reads the body renders the mid-read case with
`AsBodyTooLarge(err)` and `WriteBodyTooLarge(w, limit)`; the
projection does, so an oversized `POST` is a `413`, never a `400`, and
never reaches the executor. `OnBodyTooLarge(hook)` observes each
refusal once, which is how the api service audits it.

The api service installs it at HTTP slot 10 of the
[middleware order](../../contracts/serve-lifecycle.md#middleware-order-on-the-http-plane):
after the request id, access log, recovery, and the Host, Origin and
CORS checks, and before compression and `Auth` — the limit costs
nothing and reveals nothing, and nothing ahead of it reads the body.
Health and metrics endpoints answer before it and are exempt.

The cap is the `body_limit` block, resolved per key: the service's
own key, then `services.all`, then `APIConfig.MaxBodyBytes`, then the
1 MiB default.

```yaml
services:
  all:
    body_limit:
      max_bytes: 2097152   # every service
  api:
    body_limit:
      max_bytes: 4194304   # api only; 0 = the default
      # enabled: false     # no cap for api
```

`max_bytes` must be a whole, non-negative number of bytes and
`enabled` a boolean; anything else, or an unknown key in the block,
fails validation at exit `2`, naming the key.

#### Server timeouts

`ServerTimeouts` holds an `http.Server`'s four timeouts;
`DefaultServerTimeouts()` is what every kit HTTP listener uses unless
configured — read header 5s, read 5s, write 10s, idle falling back to
read. `Apply(srv)` sets them, `ServerTimeoutsOf(srv)` reads them back.

The write timeout is sized for request/reply. A route whose responses
are streams lifts it for its own response with
`http.ResponseController.SetWriteDeadline(time.Time{})`;
`LiftWriteDeadline(h)` does that for every response `h` writes. The
projection's stream routes, the rpc service's `InvokeStream` and the
whole mcp endpoint are exempt this way; everything else keeps it.

`ReleaseStalledOnShutdown(srv)` keeps a stalled client from holding
`Shutdown`: when it begins, a connection whose first request's headers
never finished is closed, and a body read still waiting on the client
is ended so its handler returns; a request whose body was read drains
untouched. It also ends a connection whose handler returned with the
body still on the wire — a refusal answered before the body — rather
than letting `net/http` wait for the rest of the body for the read
timeout. Call it once `srv` has its `Handler` and `ConnState`; the api,
rpc and mcp services install it with their timeouts.

The api, rpc and mcp services read their timeouts from the `timeouts`
block, per key: the service's own key, then `services.all`, then the
default.

```yaml
services:
  all:
    timeouts:
      idle: 2m            # every service
  api:
    timeouts:
      read_header: 5s
      read: 30s           # slow uploads
      write: 10s
      command: 1m         # per-command deadline; see below
```

Every value is a duration (`5s`, `2m`); `0` means none (a zero
`read_header` or `idle` falls back to `read`, as in `net/http`). A
negative value, a bare number, or an unknown key fails validation at
exit `2`, naming the key. `command` is the per-command deadline for
commands that declare no `kit/timeout`, answered `504`
`deadline_exceeded` when it passes; it reaches the socket service too.
See [Deadlines](cmdsurface.md#deadlines).

#### Claims and identity

`Auth` stores whatever claims the `AuthFunc` returns; the projection
never interprets them. To attribute a call it asks `IdentityOf`, which
understands three shapes without importing an adopter's types:

| Claims value | Principal | Tenant |
|---|---|---|
| implements `Identity` | `Principal()` | `TenantID()` |
| `Claims` (or `*Claims`) | `Subject` | `Tenant` |
| string-keyed map (any element type, named types included) | `"sub"` | `"tenant"` |

Anything else authenticates the call and leaves it unattributed.
`ScopesOf` reads `Claims.Scopes`, or from a map the first of
`"scopes"` (a list, or one scope), `"scope"` (space-delimited, as OAuth
issuers mint it) and `"scp"` (a list or space-delimited).
`Auth(fn, OnAuthRefused(hook))` lets a refusal be observed
before the `401` is written, which is how the api service records it
in the audit trail.

#### Bearer-token verifiers

`go/transport/authn` builds ready `AuthFunc`s that verify a JWT from
`Authorization: Bearer`: `NewJWT` over fixed keys (`IdentityKey` for
the tool's identity keypair, `ParsePublicKeyPEM` for key files),
`NewJWKS` over a key set URL, `NewOIDC` over an issuer's discovery.
Each takes `Options` (issuer, audience, clock skew, tenant claim, a
`Check` revocation hook) and returns a `*Verifier`; `AuthFunc()`
returns `Claims` with the principal, tenant and scopes. The
kit-shipped services build one from `services.<svc>.auth.mode: jwt`,
`jwks` or `oidc`; see the
[serve-lifecycle contract](../../contracts/serve-lifecycle.md#bearer-tokens).

#### Client certificates

`ClientCertAuth(ClientCertConfig{...})` is an `AuthFunc` that reads
the client certificate the TLS handshake verified and returns a
`Claims`: the principal from `Principal` (`PrincipalSAN`, the
default: first URI, else DNS, else email SAN; or `PrincipalSANURI`,
`PrincipalSANDNS`, `PrincipalSANEmail`, `PrincipalCN`), the tenant
from `TenantOID` (a subject attribute, else an extension holding a
string) or `TenantSAN` (first capture group of the first SAN it
matches). A request without a verified certificate, or whose
certificate has no principal there, is refused. The server's
`tls.Config` does the verifying (`ClientCAs`, `ClientAuth`);
`ClientCertClaims` is the extraction alone.

Set `http.Server.ConnContext` to `TLSConnContext` when the `AuthFunc`
may see a synthetic request, as under `rpc.Authenticate`: `TLSState(r)`
then finds the connection's TLS state through the request context.
The kit-shipped services set both from `services.<svc>.auth.mode: mtls`;
see the [serve-lifecycle contract](../../contracts/serve-lifecycle.md#tls-and-client-certificates).

#### Request provenance

Each projected call gathers a `RequestMeta` and hands it to the
executor on `CommandRequest.Meta`:

| Field | Source |
|---|---|
| `Authenticated` | `Authenticated(ctx)`: `Auth` verified the request |
| `Principal`, `Tenant`, `Scopes` | the stored claims |
| `RequestID` | the `RequestID` middleware (`X-Request-ID`, issued when absent, echoed) |
| `TraceID` | the trace-id field of `traceparent`, else `X-Trace-ID` |
| `Traceparent`, `Tracestate` | the W3C headers, when `traceparent` is well-formed (`TraceContextFromHeader`) |
| `IdempotencyKey` | `Idempotency-Key`; see replay above |
| `RemoteAddr`, `ReceivedAt` | the request |

The request's own context is passed through unchanged, so a client
disconnect cancels the command. The executor maps all of this onto
`cmdsurface.Meta`; scopes travel as `Meta.Extra["scopes"]`,
comma-joined, for the permission gate.

#### Refusal codes for metrics

Middleware that refuses a request on the HTTP plane calls
`RecordRefusal(r, code)` with the stable refusal code
(`body_too_large`, `host_rejected`, …) before writing the response.
An observer installed further out with `ObserveRefusal(r)` reads the
code once the handler returns; the tracing and metrics middleware
([served-observability.md](served-observability.md)) is that observer.
With no observer, recording is a no-op, and the first code recorded
for a request wins.

`ObserveRoute(r)` works the same way for the route a `Router`
matched: middleware that wraps the router from outside never sees
`Request.Pattern`, which the mux sets on the request it dispatches.
The tracing and metrics middleware reads it back to name its span
`<METHOD> <route>`. An empty read means no route matched: a health
probe, a refusal ahead of the router, or an unmatched path.

### Transport guards

Three middlewares keep a browser from reaching the server on a page's
behalf. The api service installs all three by default (keys and
defaults in
[secure-remote-serving.md](../guides/secure-remote-serving.md#8-keep-browsers-out-host-origin-response-headers));
an adopter serving a router of its own composes them the same way:

```go
hosts, wildcard := api.ListenerHosts(addr)
origin, err := api.OriginCheck(api.OriginCheckConfig{})
if err != nil {
    return err
}
guards := []api.Middleware{api.SecurityHeaders(api.SecurityHeadersConfig{})}
if !wildcard {
    guards = append(guards, api.HostCheck(api.HostCheckConfig{Allow: hosts}))
}
guards = append(guards, origin)
srv := &http.Server{Addr: addr, Handler: api.Chain(guards...)(router)}
```

Wrap the whole handler, never `WithMiddleware`: router middleware runs
per route, and huma's own routes (`/docs`, `/openapi.json`,
`/schemas`), `/capabilities` and unmatched paths never pass through
it.

| Middleware | Refuses | Behavior |
|---|---|---|
| `HostCheck(HostCheckConfig{Allow})` | `403` `host_rejected` | `Host` must match an `Allow` entry: `name`, IP literal, or either with `:port` (an entry without a port matches any). Case and a trailing dot are ignored; `"*"` accepts all. No `Host` (HTTP/1.0) passes. |
| `OriginCheck(OriginCheckConfig{Allow})` | `403` `origin_rejected` | `http.CrossOriginProtection`: safe methods, requests with neither `Origin` nor `Sec-Fetch-Site`, and same-origin requests pass; `Allow` adds cross-origin `scheme://host[:port]` origins; `"*"` disables. Errors on a malformed entry. |
| `SecurityHeaders(SecurityHeadersConfig{})` | — | Sets `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Content-Security-Policy` (`DefaultContentSecurityPolicy`, or `ContentSecurityPolicy`), and, when `IsHTTPS(r)` (TLS here, or `https` a trusted proxy forwarded to `ClientAddress`), `Strict-Transport-Security` (`HSTSMaxAge`, default one year; negative disables). A value an outer layer set is kept; a handler replaces the CSP with `Header().Set`. The `ResponseWriter` passes through, so SSE and WebSocket upgrades are unaffected. |
| `ClientAddress(ClientAddressConfig{TrustedProxies})` | — | Slot 2. From a peer in `TrustedProxies` (`ParseTrustedProxies`: CIDRs or addresses), walks `Forwarded`, else `X-Forwarded-For`, else `X-Real-IP` right to left past trusted hops; the first untrusted address becomes `r.RemoteAddr` (bare IP), the proxy `PeerAddrFromContext`, and a forwarded `https` makes `IsHTTPS` true. Any other peer, or a header that does not parse in full or disagrees with the other, leaves the request as it came. Empty `TrustedProxies` installs nothing. |

`ListenerHosts(addr)` derives the allowlist a listen address implies:
`LoopbackHosts` (`localhost`, `127.0.0.1`, `::1`) plus the host for a
loopback bind, the host itself for a named or IP bind, and
`wildcard == true` with no hosts for `0.0.0.0`, `::` or an empty host.

Both checks write an `APIError` body by default. `Refuse` on either
config takes a `RefusalWriter` for a protocol that reports errors
elsewhere; `mcpsdk` uses it to answer with a JSON-RPC error. Put Host
before Origin: the Origin check's "same origin" compares against
`Host`, so it means a host the server answers for only once Host is
checked. A WebSocket upgrade is a `GET`; `WSHandler` applies its own
same-origin check (`WithAcceptOrigins`).

### OpenAPI

With `WithOpenAPI` configured, every projected operation is described
at `/openapi.json`: parameters or request body per the method, the
declared output schema on `data` where the adopter declared one, the
confirmation flags where a command is gated (`confirm` as an enum of
its accepted values), and `[destructive]` in the summary
so danger is visible in a generated client's method list. Every
operation carries `x-kit-side-effect` and `x-kit-side-effect-source`,
the discovery fields, in the minimal spec too. A read the
[result cache](#result-cache) answers also declares `If-None-Match`,
`ETag`, `Cache-Control` and `304`, in both specs. Operation
ids are `commands_<path_with_underscores>`.

Only handlers registered on the raw router serve traffic; the huma
registration describes, so middleware wraps exactly one path.

Without `WithOpenAPI`, projection still mounts, and a **minimal** spec
is served at the same `/openapi.json` — enough to find every operation,
its method and its path. Full schemas are what `WithOpenAPI` buys.

A request/reply route answers when the command finishes; for output
as it is written, use the command's [streaming route](#streaming).

## Health and readiness

The api service answers two probe routes, for orchestrators and load
balancers:

| Route | Question | `200` when | `503` when |
|---|---|---|---|
| `GET /healthz` | Liveness: is the process serving HTTP? | always, while it answers | never |
| `GET /readyz` | Readiness: should it get traffic? | the api service is ready AND every `APIConfig.DependsOn` service the run started is ready | before the listener reports ready, once `Stop` begins draining, or while a dependency is starting, failed, stopped, or not ready |

Both answer `GET` and `HEAD` (`405` otherwise), send
`Cache-Control: no-store`, and return a minimal body:

```json
{"status":"ok"}
{"status":"unavailable","failing":["api","store"]}
```

`failing` names the checks that failed, and nothing else: no version,
no path, no error text. It is present by default on a loopback bind
only; `services.api.health.detail` turns it on or off explicitly.

A dependency counts as ready when the serve supervisor recorded it
ready and its own `Ready` still agrees, so a dependency that crashed
under the `isolate` failure policy fails readiness even if it never
reset its own flag. A dependency the run did not start (`serve api`
alone) is not checked, the same way it does not constrain start
order.

**What a probe skips.** The routes are answered in front of the
router, not registered on it, and the request ends there. They sit
inside the request id, access log, recovery, telemetry and security
header layers, and in front of everything else: the `Host` and
`Origin` checks (orchestrators address a pod by IP), the body limit,
`APIConfig.Auth`. The routes invoke no command, so nothing on the
invocation plane — the permission gate, rate limits, audit — sees
them either. The same wrapper is available outside the api service
as `api.HealthRoutes(next, api.HealthConfig{...})`.

**What a probe is not.** The routes are not commands and not API
operations: they appear in neither `GET /v1/commands` nor
`/openapi.json` nor the capabilities listing, so a generated client
does not grow health methods, and moving them with `path_prefix`
changes no published contract.

| Key | Default | Effect |
|---|---|---|
| `services.api.health.enabled` | `true` | `false` serves neither route; the paths fall through to the router |
| `services.api.health.path_prefix` | `""` | Mount under a prefix: `/_kit` serves `/_kit/healthz` and `/_kit/readyz`. Absolute, no trailing slash; anything else is refused at validation, exit `2` |
| `services.api.health.detail` | `true` on loopback, else `false` | Name failing checks in the `503` body |

Each key may also be set once under `services.all.health`; the api's
own key wins, key by key. Any other key in either `health` block is
refused at validation, exit `2`.

An adopter route registered at exactly a probe path — through
`APIConfig.Handlers` or `Resources` — wins, and kit answers only the
probe the adopter left alone. A subtree mount at `/` does not claim
the probe paths.

## Response compression

`api.Compress()` encodes response bodies with zstd or gzip, whichever
the request's `Accept-Encoding` ranks higher (zstd on a tie), and
sends identity when it accepts neither. The api service puts it in
its middleware chain after the body limit and before auth, and only
when enabled:

```yaml
services:
  api:
    compression:
      enabled: true      # default false, on loopback and beyond it
      min_bytes: 1024    # default api.DefaultCompressMinBytes
```

Either key may be set once for every service under
`services.all.compression`; the service's own key wins. Compression
is off by default: a loopback client gains nothing, and a reverse
proxy in front of the service compresses for the clients beyond it.
An unknown key in either block, or a negative `min_bytes`, is refused
at validation, exit `2`.

A response is encoded only when all of these hold:

| Condition | Why |
|---|---|
| Content-Type on the allowlist: JSON, YAML, XML (with `+json`, `+yaml`, `+xml` suffixes, so the OpenAPI and problem types), every `text/*` except `text/event-stream` | binary and pre-compressed bodies do not shrink |
| At least `min_bytes`, from `Content-Length` or what was written | a body that fits one packet gains nothing |
| The handler set no `Content-Encoding` or `Content-Range` | a body is encoded once |
| The request is not `HEAD` | a `HEAD` answer carries identity headers |

Every response it considers carries `Vary: Accept-Encoding`. An
encoded response drops the handler's `Content-Length` and
`Accept-Ranges`, which no longer describe the body.

Passed through untouched, with no `Vary`:

- WebSocket upgrades and `CONNECT`.
- Event streams: a request that accepts `text/event-stream`, and any
  response whose Content-Type is `text/event-stream`. Every flush,
  including the one that sends the headers before the first event,
  reaches the client at once.
- Connect, gRPC and gRPC-Web requests, which negotiate compression
  per message themselves. On the RPC server that is
  `cmdsurface.WithRPCCompression(minBytes)`, off unless passed to
  `MountRPC`.

A streaming response of an allowlisted type is not held back:
`Flush` settles the decision and flushes the encoder and the
connection. The writer implements `http.Flusher` and `Unwrap`, so
`http.ResponseController` reaches deadlines and hijacking. MCP routes
mounted on the router with `MountMCP` are covered the same way: JSON
replies are encoded, streamed replies pass through.

`WithCompressMinBytes(n)` and `WithCompressFilter(fn)` tune the
middleware for a router you assemble yourself.

## Result cache

A read command can be answered without running: declare how long one
result stays good, and the api service serves it from a cache for that
long, with a validator the client can revalidate against.

```go
list := &cobra.Command{
	Use:         "list",
	Annotations: map[string]string{"kit/side-effect": "read"},
	RunE:        listWidgets,
}
cli.SetCacheTTL(list, 30*time.Second) // kit/cache-ttl: 30s
```

```bash
curl -si 'http://127.0.0.1:8080/v1/commands/widget/list?limit=5'
# HTTP/1.1 200 OK
# Cache-Control: public, max-age=30
# Etag: W/"9f2c…"

curl -si -H 'If-None-Match: W/"9f2c…"' 'http://127.0.0.1:8080/v1/commands/widget/list?limit=5'
# HTTP/1.1 304 Not Modified
```

What is cached, and for whom:

| Rule | Detail |
|---|---|
| Only reads | the command declares `kit/side-effect: read` itself. Write, destructive, interactive, unannotated and name-inferred commands are never cached; `kit/cache-ttl` on one is refused at `Root.Validate` |
| Only successes | exit code `0` and no error; a failure is never stored or shared |
| One entry per call and caller | the key is the command path, flags (in any order) and args, plus the principal, tenant and scopes the transport verified (`Auth`, a client certificate). A caller the transport itself vouches for is keyed by its transport, never by the name it claims. A caller or tenant that is only claimed is keyed as anonymous. One caller is never answered with another's result, and each gets its own ETag |
| `Cache-Control` | `max-age` is what is left of the TTL; `private` when the call carries a verified principal, tenant or scopes, `public` otherwise |
| `ETag` | weak (`W/"…"`), so a compressed response keeps it. `If-None-Match` with it, or `*`, answers `304` and no body |
| Identical calls in flight | wait for the one already running and share its result, instead of running again. A waiter whose client leaves stops waiting; the run goes on |
| Audit | every call is audited; one answered without running carries `cache: "hit"` (from the store) or `cache: "coalesced"` (shared a run in flight) |
| Streams | the `/stream` route of a cached read answers a stored result as its final frame; a miss streams live and stores nothing |
| OpenAPI | `/openapi.json` declares the cache on each read it answers: an optional `If-None-Match` header, `ETag` and `Cache-Control` on `200`, and a `304` with no body. The full and the [minimal](#openapi) spec say the same. A read without `kit/cache-ttl`, or a service with the cache off, declares none of it. The `/stream` operation is unchanged: a cached answer is the same event stream, with no `event` frames before the terminal `result` |

A claimed identity shares the anonymous entry, so a command whose
output depends on who calls — it reads `Meta.Caller`, the tenant or
the scopes — either declares `kit/auth-required`, so every call
carries a verified identity and gets its own entry, or declares no
`kit/cache-ttl`. Otherwise, on a listener that does not authenticate,
two callers claiming different identities are answered with one
result.

The cache is on by default and does nothing until a command declares
`kit/cache-ttl`. Its block:

```yaml
services:
  api:
    cache:
      enabled: true        # default
      backend: memory      # default; bounded, emptied on restart
      max_bytes: 67108864  # memory only; default 64 MiB, least recently used evicted
      path: ""             # file or directory for a file backend
```

Every key may also be set under `services.all.cache`. Only the api
service applies the block: set under any other service, it is refused
at validation, exit `2`. `backend` names
a `kv` driver that stores with a TTL: `memory`, or `sqlite` or `badger`
once the binary imports `hop.top/kit/go/storage/kv/sqlite` (or
`.../badger`), which then need `path`. A file backend keeps results
on disk across restarts; point `path` somewhere only the service can
read. An unknown key, an unregistered backend, `etcd` or `tidb` (no
TTL), a file backend without `path`, or `max_bytes` on anything but
`memory` is refused at validation, exit `2`.

A bridge you assemble yourself turns the cache on with
`cmdsurface.WithResultCache(store)`; [`MountProjection`](cmdsurface.md)
then renders the headers and declares them in the spec. An executor of
your own sets `CommandResult.Cache` (an `api.CacheDirective`) for the
same effect, and `CommandDescriptor.Cacheable` on the command so the
spec declares it.

## Related pages

- [expose-cli-over-rest.md](../guides/expose-cli-over-rest.md): the
  task walkthrough
- [secure-remote-serving.md](../guides/secure-remote-serving.md):
  auth beyond loopback, the permission gate, the audit trail
- [cmdsurface reference](cmdsurface.md): the bridge, the policy gate,
  every other surface
- [serve lifecycle contract](../../contracts/serve-lifecycle.md)
