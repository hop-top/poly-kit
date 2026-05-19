# cmdsurface

One cobra tree, many transports.

## Why

Adopters who build a kit-based binary already get a unified command tree
via cobra (see `go/console/cli/`). That tree is projected onto the CLI
surface (the binary itself) and the MCP surface (via
`go/ai/toolspec/adapters`). Adopters who also want REST, ConnectRPC, a
WebSocket fan-out, SSE streaming, webhook ingress, bus subscribers,
cron, OAuth callbacks, signed one-shot exec links, or a FaaS deploy end
up writing the same command logic three or four times — once per
transport, kept in sync by hand.

`cmdsurface` removes the duplication. A `Bridge` wraps a `*cobra.Command`
root; each `Mount*` function projects the leaves of that tree onto one
transport. The same safety annotations (`kit/side-effect`,
`kit/auth-required`, `kit/requires-confirmation`) gate every surface
through a single `Policy`. The same `Runner` executes every invocation.
A leaf's enablement on each surface is a per-leaf toggle, controllable
in YAML or via `Bridge.Expose` / `Bridge.Hide`.

The package layers on top of existing kit transports rather than
replacing them: REST uses `api.Router`, RPC uses `connectrpc.com/connect`
on top of `transport/rpc`, MCP serves JSON-RPC over the same router,
Bus consumes adopter-supplied `Subscriber` impls, and FaaS adapters wrap
the same Runner under provider invocation contracts.

## Concepts

- **Bridge** — wraps a cobra root, owns the `Runner` and `Policy`, and
  tracks per-leaf surface enablement.
- **Leaf** — one runnable cobra command in the tree, discovered at
  `New` time. Carries its `Path`, the resolved `*cobra.Command`, a
  snapshot `SafetyClass`, and a per-surface `Enabled` map.
- **Surface** — a transport projection identified by a string constant
  (`SurfaceREST`, `SurfaceMCP`, etc.). Thirteen surfaces are declared.
- **Invocation** — the transport-agnostic call envelope: `Path`,
  `Args`, `Flags`, `Meta`. Every surface decodes its wire format into
  this shape.
- **Result** — the unified return value (`ExitCode`, `Stdout`,
  `Stderr`, optional `Data`). Surfaces map it onto their wire format.
- **Event** — one streaming frame (`stdout` / `stderr` / `progress` /
  `done`) produced by `Runner.Stream`.
- **Runner** — executes an `Invocation`. The default
  `InProcessRunner(root)` re-enters the cobra tree in-process;
  `SubprocessRunner(binary)` is the stub for sandboxed execution.
- **Sink** — fan-out target for completed invocations (log, file,
  webhook, bus). Orthogonal to surfaces.
- **Policy** — the destructive ceiling. Conservative default: no remote
  surface can invoke a `kit/side-effect=destructive` leaf unless the
  surface is listed in `Policy.AllowDestructiveOn`.
- **Mapping** — adopter-supplied binding from an external trigger
  (webhook slug, Lambda event, signed token) to a leaf, with optional
  template-driven flag extraction.

## Surface matrix

| Surface         | Direction           | Mount function   | Typical use                             | Reuses                          |
|-----------------|---------------------|------------------|-----------------------------------------|---------------------------------|
| `cli`           | local invocation    | (cobra itself)   | adopter's binary                        | `go/console/cli`                |
| `rest`          | request / reply     | `MountREST`      | machine-to-machine RPC over HTTP        | `api.Router` + huma             |
| `ws`            | bidirectional       | `MountWS`        | interactive streaming clients           | `api.Hub` + `coder/websocket`   |
| `sse`           | server-stream       | `MountSSE`       | one-way streaming to browsers           | `api.Router`                    |
| `rpc`           | request / reply + server-stream | `MountRPC`    | strongly-typed Go/JS clients            | `transport/rpc` (ConnectRPC)    |
| `mcp`           | discovery + exec    | `MountMCP`       | LLM tool calls                          | MCP JSON-RPC over `api.Router`  |
| `webhook`       | inbound HTTP        | `MountWebhooks`  | third-party push (GitHub, Stripe, …)    | `api.Router` + `text/template`  |
| `bus`           | pub/sub             | `MountBus`       | async workflows, fan-in                 | `transport/api.EventPublisher`  |
| `cron`          | scheduled           | `MountCron`      | recurring jobs                          | `robfig/cron/v3` (pluggable)    |
| `lib`           | in-process Go API   | `InvokeArgs` / `StreamArgs` | REPLs, tests, internal automation | the `Runner` itself             |
| `oauth-cb`      | inbound HTTP        | `MountOAuth`     | OAuth provider callback                 | `api.Router` + `StateStore`     |
| `signed`        | inbound HTTP        | `MountSigned`    | one-shot magic-link exec                | `api.Router` + `NonceStore`     |
| `faas`          | provider-driven     | `LambdaHandler` / `RunCloudRun` | Lambda + Cloud Run               | aws-lambda-go, `net/http`       |

## Quick start

```go
import (
    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
)

// 1. Build the cobra tree (your existing CLI).
root := buildCobraTree()

// 2. Build the Bridge.
b := cmdsurface.New(root)

// 3. Expose leaves on the surfaces you want, then mount.
b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP, cmdsurface.SurfaceWS)

r := api.NewRouter()
_ = cmdsurface.MountREST(b, r)
_ = cmdsurface.MountMCP(b, r)
_ = cmdsurface.MountWS(b, r)

// 4. Serve.
_ = http.ListenAndServe(":8080", r)
```

Three lines per surface. Same cobra tree, same handlers, same policy.

## Per-surface reference

### REST

```go
func MountREST(b *Bridge, r *api.Router, opts ...RESTOption) error
```

Wire shape: `POST {prefix}/{path}` with a JSON `Invocation` body,
returns a JSON `Result`. `prefix` defaults to `/cmd`; path segments are
the leaf's cobra path joined with `/`.

Options:

- `WithRESTPrefix(prefix string)` — change the URL prefix.
- `WithRESTAuth(api.AuthFunc)` — wire auth for leaves where
  `Class.AuthRequired` is true.
- `WithRESTMiddleware(...func(http.Handler) http.Handler)` — install
  outermost middleware.
- `WithRESTOpenAPI(humaAPI any)` — register one OpenAPI operation per
  mounted leaf (no second handler installed).

```bash
curl -X POST http://localhost:8080/cmd/widget/add \
  -H 'content-type: application/json' \
  -d '{"flags":{"name":"foo"}}'
```

Sentinel-error mapping: `ErrUnknownCommand` → 404 `unknown_command`,
`ErrSurfaceNotEnabled` → 404 `not_enabled`, `ErrDestructiveBlocked` →
403 `destructive_blocked`. Confirmation-required leaves require an
`X-Confirm-Token` header (presence-only, value not validated). See
`go/transport/cmdsurface/surface_rest_test.go`.

### RPC

```go
func MountRPC(b *Bridge, s rpcServerMount, opts ...RPCOption) error
```

Wire shape: ConnectRPC at `RPCServicePath = "/cmdsurface.v1.Commands/"`.
Two procedures: `Invoke` (unary) and `InvokeStream` (server-streaming).
Wire codec is JSON over arbitrary Go values — `Invocation`, `Result`,
and `Event` are plain structs with JSON tags, not proto messages.
Clients must pass `RPCClientOptions()` to `connect.NewClient`.

Options:

- `WithRPCInterceptors(ic ...connect.Interceptor)` — append
  interceptors on top of the server's own.

Per-leaf gates: `Authorization` header (or `inv.Meta.Caller`) when
`Class.AuthRequired`; `X-Confirm-Token` header when
`Class.RequiresConfirmation`. Error mapping: unknown / not-enabled →
`CodeNotFound`, destructive-blocked → `CodePermissionDenied`,
unauthenticated → `CodeUnauthenticated`. See
`go/transport/cmdsurface/surface_rpc_test.go`.

### MCP

```go
func MountMCP(b *Bridge, r *api.Router, opts ...MCPOption) error
```

Wire shape: MCP JSON-RPC 2.0 at the configured path (default `/mcp`).
Supports `initialize`, `tools/list`, `tools/call`. Tool name is the
dotted leaf path (e.g. `widget.add`). Flag schema is derived from the
leaf's pflag set; `Result.Stdout` becomes a text content block, non-zero
`ExitCode` sets `isError: true`.

Options:

- `WithMCPPath(path string)` — override `/mcp`.
- `WithMCPServerInfo(name, version string)` — identity returned by
  `initialize`.

Protocol reference: <https://modelcontextprotocol.io/specification>.
See `go/transport/cmdsurface/surface_mcp_test.go`.

### WebSocket

```go
func MountWS(b *Bridge, r *api.Router, opts ...WSOption) error
```

Wire shape: GET `/ws/cmd` upgrade. JSON frames in both directions:

```
client → server  {"op":"invoke","id":"<corr-id>","invocation":{...}}
client → server  {"op":"cancel","id":"<corr-id>"}
server → client  {"op":"event","id":"<corr-id>","event":{...}}
server → client  {"op":"result","id":"<corr-id>","result":{...}}
server → client  {"op":"error","id":"<corr-id>","error":{"code":"...","message":"..."}}
```

Options:

- `WithWSPath(path string)` — override `/ws/cmd`.
- `WithWSHub(*api.Hub)` — bring your own hub (caller owns lifecycle).
- `WithWSContext(ctx context.Context)` — bound the lifetime of the hub
  goroutine MountWS starts when no hub is supplied.
- `WithWSAcceptOrigins(origins ...string)` — allow non-same-origin
  upgrades.

Safety gates fire at upgrade time using the aggregate matrix of every
WS-enabled leaf (strictest wins). Per-invocation policy gates fire on
each `invoke` frame. See `go/transport/cmdsurface/surface_ws_test.go`.

### SSE

```go
func MountSSE(b *Bridge, r *api.Router, opts ...SSEOption) error
```

Wire shape: `GET {prefix}/{path}/stream?arg=<v>&flag.<name>=<v>`.
Response is `text/event-stream` carrying `event` frames followed by
exactly one terminal `result` (success) or `error` (failure) frame.
A 15-second comment heartbeat keeps idle streams alive through proxies.

Options:

- `WithSSEPrefix(prefix string)` — change the URL prefix.
- `WithSSEAuth(api.AuthFunc)` — auth for leaves with
  `Class.AuthRequired`.
- `WithSSEMiddleware(...func(http.Handler) http.Handler)` — install
  outermost middleware.

```bash
curl -N 'http://localhost:8080/cmd/widget/list/stream?flag.format=json'
```

Pre-stream sentinel errors map to HTTP status codes (404 / 403 / 401 /
428). Once the stream has begun, every further error is an `event:
error` frame. See `go/transport/cmdsurface/surface_sse_test.go`.

### Bus

```go
func MountBus(
    b *Bridge,
    sub Subscriber,
    pub api.EventPublisher,
    bindings []BusBinding,
    opts ...BusOption,
) (cleanup func(), err error)
```

Wire shape: per `BusBinding`, the surface subscribes to `RequestTopic`,
decodes each message payload as `{args, flags, meta}` JSON, invokes the
bridge, and publishes the `Result` (or error envelope) to
`ResponseTopic` when set. `Subscriber` is the adopter's pub/sub adapter
(Kafka, NATS, Redis Streams, in-process). Per-message gates inspect
`msg.Headers["authorization"]` and `msg.Headers["x-confirm-token"]`.

Options:

- `WithBusContext(ctx context.Context)` — parent context for every
  subscription.
- `WithBusLogger(fn)` — printf-style logger for non-fatal errors.

All application failures (decode, unknown leaf, destructive-blocked,
runner error) are conveyed as `{"error":{"code":"...","message":"..."}}`
on the response topic. The handler returns `nil` to the subscriber in
every case — bus protocols do not signal app errors via redelivery. See
`go/transport/cmdsurface/surface_bus_test.go`.

### Cron

```go
func MountCron(
    b *Bridge,
    engine CronEngine,
    schedules []CronSchedule,
    opts ...CronOption,
) (cleanup func(), err error)
```

Wire shape: per `CronSchedule`, the engine fires at `Expr` in
`Timezone`, calling `Bridge.Invoke` with `Meta.Caller = "cron"`. The
default engine is `DefaultCronEngine()`, backed by `robfig/cron/v3`
(5-field expression, per-schedule timezone via `CRON_TZ=` prefix).
`AuthRequired` leaves are refused at mount unless opted in with
`WithCronAllowAuth(true)`.

Options:

- `WithCronContext(ctx context.Context)` — context handed to each
  `Bridge.Invoke`.
- `WithCronResultSink(fn)` — callback for each completed run.
- `WithCronAutostart(autostart bool)` — control engine.Start lifecycle.
- `WithCronLogger(fn)` — printf-style diagnostic logger.
- `WithCronAllowAuth(allow bool)` — permit auth-required leaves.

Adopters wanting River, Temporal, or a hosted scheduler implement
`CronEngine` and pass it instead of `DefaultCronEngine()`. See
`go/transport/cmdsurface/surface_cron_test.go`.

### Library (in-process Go API)

```go
func InvokeArgs(ctx context.Context, b *Bridge, argv []string, opts ...InvokeOption) (Result, error)
func StreamArgs(ctx context.Context, b *Bridge, argv []string, out chan<- Event, opts ...InvokeOption) error
```

Parses `argv` as the same shape cobra parses on the command line,
forces `Meta.Surface = SurfaceLib`, and dispatches via the bridge.

Options:

- `WithFlag(name string, value any)` — override / inject a typed flag.
- `WithCaller(string)` — set `Meta.Caller`.
- `WithTraceID(string)` — set `Meta.TraceID`.
- `WithExtra(key, value string)` — add to `Meta.Extra`.

```go
res, err := cmdsurface.InvokeArgs(ctx, b,
    []string{"widget", "add", "--name", "foo"},
    cmdsurface.WithCaller("test"))
```

See `go/transport/cmdsurface/surface_lib_test.go` and
`go/transport/cmdsurface/example_lib_test.go`.

### Webhook (inbound)

```go
func MountWebhooks(b *Bridge, r *api.Router, mappings []WebhookMapping, opts ...WebhookOption) error
```

Wire shape: `POST {prefix}/{Name}` per mapping. The handler reads up to
`WithWebhookMaxBody` (default 1 MiB), runs `mapping.Auth.Verify(r, body)`
(`AuthNone` / `AuthHMAC` / `AuthBearer`), executes `FlagMap` and
`ArgsTemplate` against a root of `{.body, .headers, .query, .path}`,
invokes the bridge, and responds 202 Accepted on success.

Options:

- `WithWebhookPrefix(prefix string)` — change the URL prefix.
- `WithWebhookMaxBody(n int64)` — cap inbound body size.
- `WithWebhookResultLog(fn)` — synchronous callback after every invoke.
- `WithWebhookAllowConfirmation()` — accept mappings on leaves with
  `Class.RequiresConfirmation` (the `Auth` scheme is the gate).

```go
m := cmdsurface.WebhookMapping{
    Name:    "widget-create",
    Path:    []string{"widget", "add"},
    FlagMap: map[string]string{"name": "{{ .body.title }}"},
    Auth: cmdsurface.AuthHMAC{
        Header: "X-Hub-Signature-256",
        Prefix: "sha256=",
        Secret: []byte(os.Getenv("WIDGET_HOOK_SECRET")),
    },
}
```

`AuthHMAC` and `AuthBearer` use constant-time comparison. Mappings
targeting auth-required leaves with `AuthNone` are refused at mount.
See `go/transport/cmdsurface/surface_webhook_test.go`.

### OAuth callback

```go
func MountOAuth(b *Bridge, r *api.Router, providers []OAuthProvider, store StateStore, opts ...OAuthOption) error
```

Wire shape: `GET {prefix}/{Name}/authorize` issues a state nonce and
redirects to the upstream provider's authorize URL; `GET
{prefix}/{Name}/callback` validates the state via `StateStore.Consume`,
extracts query parameters into flags per `FlagFromQuery`, and invokes
the leaf. Validated state IS the authentication — the bridge skips
`Class.AuthRequired` gates on the OAuth callback surface.

Options:

- `WithOAuthPrefix(prefix string)` — change the URL prefix.
- `WithOAuthStateTTL(d time.Duration)` — state lifetime (default 10m).
- `WithOAuthAuthorizeFn(fn func(provider string) (string, error))` —
  return the upstream provider's authorize URL.

`InMemoryStateStore` is provided for single-process adopters; multi-
replica deployments wire a shared store. Leaves with
`Class.RequiresConfirmation` are refused at mount (redirect flow has
no token-prompt surface). See
`go/transport/cmdsurface/surface_oauth_test.go`.

### Signed URL

```go
func MountSigned(b *Bridge, r *api.Router, key []byte, store NonceStore, opts ...SignedOption) error
```

Wire shape: `GET {prefix}/{token}` (default prefix `/x`). The token is
`base64url(SignedToken JSON) "." base64url(HMAC-SHA256 tag)`. The
verifier consumes the nonce via `store.Consume`, builds the invocation
from the token's baked-in `Path` / `Args` / `Flags`, and dispatches.
Issuance is separate from verification (`SignedIssuer.Issue` /
`IssueViaBridge`) — a job worker can mint URLs while only the public
router mounts the verifier.

Options:

- `WithSignedPrefix(prefix string)` — change the URL prefix.
- `WithSignedSuccessRedirect(url string)` — respond with 302 instead of
  200 + JSON on success.
- `WithSignedErrorRedirect(url string)` — respond with 302
  `?error=<code>` instead of the default plain JSON error.

The signed URL IS the auth (effectively a bearer token):
`Class.AuthRequired` and `Class.RequiresConfirmation` gates are skipped.
Destructive leaves still require `Policy.AllowDestructiveOn` to include
`SurfaceSigned` — otherwise MountSigned refuses to mount.
`InMemoryNonceStore` is provided for single-process adopters; multi-
replica deployments need a shared backend (Redis, DB). See
`go/transport/cmdsurface/surface_signed_test.go`.

### FaaS — AWS Lambda

```go
func LambdaHandler(b *Bridge, cfg LambdaConfig) (func(ctx context.Context, event json.RawMessage) (json.RawMessage, error), error)
```

Returns a Lambda handler closure. `LambdaConfig.Event` selects the
event family:

- `EventAPIGatewayV2`, `EventAPIGatewayV1` — HTTP-triggered.
- `EventEventBridge` — scheduled / event-rule.
- `EventSQS` — one bridge call per record, with per-record
  `BatchItemFailures` for redelivery.
- `EventDirect` — the event JSON IS the `Invocation`.

`Mapping` (ignored for `EventDirect`) declares the leaf and
template-driven flag / args extraction; the template engine matches the
Webhook surface. Validation happens at handler-build time: unknown
leaves, leaves without `SurfaceFaaS` enabled, destructive leaves
without policy opt-in, and confirmation-required leaves all return
errors. The bridge captures into the closure once and is reused across
warm invocations. See
`go/transport/cmdsurface/adapter_lambda_test.go`.

### FaaS — Cloud Run

```go
func RunCloudRun(b *Bridge, cfg CloudRunConfig) error
```

Starts a Cloud Run-shaped HTTP server: reads `$PORT` (override via
`cfg.Port`), serves until SIGTERM with `cfg.ShutdownGrace` (default 9s),
and mounts the surfaces named in `cfg.Surfaces` (`REST`, `SSE`, `MCP`,
`WS`). Webhook / OAuth / Signed require adopter-supplied router state
and are not auto-mounted — adopters that want them build the
`*api.Router` and pass it via `cfg.Router`. See
`go/transport/cmdsurface/adapter_cloudrun_test.go`.

## Sinks

Sinks are orthogonal fan-out targets. `Sink.Emit(ctx, inv, res, err)`
is the contract; `SinkSet` is a slice of `SinkSpec` filters (by
surface, path pattern, success/error). The package does NOT call sinks
automatically — adopters wrap their `Runner` with a thin adapter that
delegates and emits (see the `sinkRunner` pattern below).

Built-in implementations:

- `LogSink` — `log/slog` records.
- `FileSink` — JSON-Lines append to an `io.Writer`.
- `WebhookSink` — POST `{invocation, result, error}` envelope to a URL,
  with optional HMAC signing via `Sign func(body) (header, value)`.
- `BusSink` — publish the same envelope via an `api.EventPublisher`.
- `TelemetrySink` — fan-out into the kit-telemetry pipeline. See the
  "Telemetry sink" section below.

## Telemetry sink

The telemetry sink fans every cmdsurface invocation completion into the
kit-telemetry pipeline so operators can observe what their binary is
doing without each adopter rebuilding identity, redaction, consent, and
transport. It is the first (and currently the only) sink type that
`FromConfig` constructs on the bridge's behalf; the other sinks remain
adopter-wired via the `sinkRunner` pattern documented above. The
telemetry sink is the exception because the kit-telemetry pipeline owns
contracts (identity, redaction, mode, consent) that should not be
re-implemented per command.

### Default disabled

`Config.Telemetry` is `nil` by default. Adopters opt in by setting
`Telemetry.Enabled = true` (in YAML or in Go) and supplying a
`TelemetryEmitterProvider`. With the block absent the bridge constructs
nothing telemetry-related — no goroutines, no consent checks, no extra
bus subscribers. A non-nil block with `Enabled = false` round-trips
through config inspection but is otherwise inert.

### Anon vs Full

`Mode: "anon"` (the default when enabled) ships only the canonical
bounded fields — `command_path`, `exit_code`, `duration_ms`,
`occurred_at`, `kit_version`, and an optional `trace_id`. Args and
flags are dropped before the event is queued, so there is no path by
which a user-supplied value can reach the wire. Anon is the right tier
for fleet health, error rates, and version tracking; it is the safe
default for telemetry returned to the kit operator.

`Mode: "full"` additionally ships the post-redact `args` and `flags`
plus a synthetic `flags["_surface"]` stamp (kit-telemetry's canonical
`Event` has no `surface` column, so the sink folds the originating
surface into `flags` rather than dropping it). Every value passes
through `telemetry.MustLoadRedactor()` inside the emitter before
publish. Full is the right tier when the adopter needs to slice on
flag values during incident response, with the trade-off that the
redactor (not the cmdsurface sink) is now the only thing between user
input and the wire.

### Size cap

`MaxBytes` (default `8192`) is the per-event ceiling applied after
translation and after redaction. The sink marshals the
`telemetry.Event` once — that JSON is the same payload the bus codec
will produce — and drops oversize events whole rather than truncating
them. Truncation could leak the prefix of a redacted token straddling
the cut point; whole-event drop is observable via
`Stats().DroppedOversize`.

### Trace correlation

`Invocation.Meta.TraceID` propagates verbatim into
`telemetry.Event.TraceID` (`omitempty`, so an unset trace ID disappears
from the wire). Surfaces that already populate `Meta.TraceID` (RPC
interceptors, REST middleware, signed-URL token claims) light up
trace-joined telemetry with no extra wiring. Adopters who want OTel
spans on top of cmdsurface invocations stamp the trace ID once in
their surface auth middleware; the sink does the rest.

### Non-blocking guarantee

`Sink.Emit` returns within ~1ms regardless of downstream pressure.
Internally the sink hands the event to a single buffered channel
(`ChannelCap` defaults to `256`) and a single drain goroutine ships
them in order to the emitter. A saturated channel surfaces as
`Stats().DroppedFull`; an emitter error (bus publish failure,
validator rejection that escapes the soft-refuse path) surfaces as
`Stats().DroppedDenied`. The producer's hot path never sees telemetry
backpressure — by design.

### Opt-in example

```go
cfg := cmdsurface.Config{
    // ... existing surfaces / commands / sinks ...
    Telemetry: &cmdsurface.TelemetryConfig{
        Enabled:    true,
        Mode:       "anon",
        ChannelCap: 256,
        MaxBytes:   8192,
    },
    TelemetryEmitterProvider: func() (*telemetry.Emitter, error) {
        // Adopters wire bus + redactor + topic prefix here.
        return telemetry.New(
            telemetry.WithBus(myBus),
            telemetry.WithTopicPrefix("myapp.telemetry.event"),
            telemetry.WithKitVersion(buildVersion),
        )
    },
}
bridge, err := cmdsurface.FromConfig(rootCmd, cfg)
if err != nil { /* ... */ }
defer bridge.Close(ctx) // flushes the drain goroutine
```

The provider is a factory (not a `*telemetry.Emitter` directly) so the
emitter and its bus are only built when the block resolves to enabled.
`Bridge.Close(ctx)` drains in-flight events through the emitter within
`ctx`'s deadline; adopters running long-lived servers should call it
during graceful shutdown.

### Verifying captured events

`kit telemetry inspect` reads spooled events post-redaction; use it to
confirm what is actually leaving the binary on a given machine before
shipping a config change. The same subcommand family
(`kit telemetry status | enable | disable | reset | inspect`) drives the
user-facing consent UX — see the adopter guide cross-link below.

### Cross-references

- kit-telemetry package: `hops/main/go/runtime/telemetry/README.md`
- Adopter consent flow + `kit telemetry` subcommands:
  `hops/main/docs/adopters/guides/telemetry.md`
- Design note (Anon/Full allow-list, size cap rationale, trace
  population status): `.tlc/tracks/cmdsurf-telemetry/design-note.md`
- Working wiring example:
  `hops/main/examples/cmdsurface/telemetry.go`

## Safety matrix

How cobra annotations gate each surface:

| Annotation                     | CLI / Lib | REST / SSE / WS    | RPC                | MCP   | Webhook              | OAuth     | Signed              | Bus                   | Cron               | FaaS                 |
|--------------------------------|-----------|--------------------|--------------------|-------|----------------------|-----------|---------------------|-----------------------|--------------------|----------------------|
| `kit/side-effect=destructive`  | allowed   | `Policy.AllowDestructiveOn` | same | same  | same                 | same      | same                | same                  | same               | same                 |
| `kit/auth-required=true`       | n/a       | `api.Auth(authFn)` (deny-all if unset) | `Authorization` header or `Meta.Caller` | n/a | `WebhookAuth.Verify` is the gate; `AuthNone` is refused | state IS auth | signed URL IS auth | `headers.authorization` or `Meta.Caller` | refused unless `WithCronAllowAuth(true)` | IAM is the gate |
| `kit/requires-confirmation=true` | n/a     | `X-Confirm-Token` header (428 when missing) | same | n/a | refused unless `WithWebhookAllowConfirmation()` | refused at mount | skipped | `headers.x-confirm-token` | (cron has no confirm channel — refused if also auth-required without opt-in) | refused at mount |
| `kit/permissions=<csv>`        | parsed; enforcement is adopter's responsibility (OAuth-scoped check before Run) |

The mappings of bridge sentinel errors to wire format are uniform:

| Bridge sentinel        | REST / SSE        | RPC               | WS / Bus / Webhook / Signed | OAuth        | Lambda            |
|------------------------|-------------------|-------------------|------------------------------|--------------|-------------------|
| `ErrUnknownCommand`    | 404 `unknown_command` | `CodeNotFound`  | `unknown_command`            | 500 (mount-time refusal) | event-type response |
| `ErrSurfaceNotEnabled` | 404 `not_enabled` | `CodeNotFound`    | `not_enabled`                | (mount-time refusal)     | (mount-time refusal) |
| `ErrDestructiveBlocked`| 403 `destructive_blocked` | `CodePermissionDenied` | `destructive_blocked` | 403 / (mount-time refusal) | (mount-time refusal) |

Cross-references: `go/transport/cmdsurface/surface_rest.go`,
`go/transport/cmdsurface/safety.go`,
`go/transport/cmdsurface/bridge.go`.

## Policy and configuration

`Policy` carries the destructive ceiling and the package-default
enablement set:

```go
type Policy struct {
    AllowDestructiveOn []Surface // surfaces on which destructive leaves are allowed
    DefaultEnabled     []Surface // per-leaf default when config omits enabled
}
```

`DefaultPolicy()` returns `{DefaultEnabled: [cli, lib, mcp]}` — no
remote destructive invocations, conservative enablement.

`Config` is the YAML shape `Load` / `LoadFile` decode and `FromConfig`
turns into a `Bridge`. The `surfaces.commands.<pattern>` map accepts
exact paths (`"widget add"`), prefix wildcards (`"widget *"`), and
catch-all (`"*"`).

```yaml
surfaces:
  defaults: [cli, lib, mcp]
  commands:
    "widget add":
      enabled: [cli, rest, ws, sse, rpc, mcp, lib]
    "widget delete":
      enabled: [cli, lib]      # destructive: locked down
    "admin *":
      enabled: [cli]

policy:
  destructive_default: deny_remote   # or "allow" to lift the ceiling
```

`destructive_default: allow` lifts the destructive ceiling for every
surface listed in a leaf's `enabled` set. The default `deny_remote`
keeps `Policy.AllowDestructiveOn` empty; programmatic callers add
specific surfaces:

```go
b := cmdsurface.New(root, cmdsurface.WithPolicy(cmdsurface.Policy{
    AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
    DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
}))
```

`WithRunner` / `WithPolicy` layered on top of `FromConfig` override
the YAML — explicit options always win.

## Adopter responsibilities

The package projects a cobra tree onto surfaces. Adopters supply:

- **Runner** — default `InProcessRunner(root)` works; swap in your own
  for sandboxing, subprocess isolation, or sink fan-out.
- **Surface selection** — call `Mount*` for the surfaces you want.
  Nothing mounts by default.
- **Webhook auth validators** — `WebhookAuth` impls. `AuthHMAC` and
  `AuthBearer` are built in; provider-specific (Stripe signature,
  Slack `v0=`, etc.) is yours to implement.
- **StateStore / NonceStore impls** — `InMemoryStateStore` and
  `InMemoryNonceStore` ship; multi-replica deployments wire Redis or
  a database.
- **CronEngine impl** — `DefaultCronEngine()` ships; River, Temporal,
  or hosted schedulers are adopter wiring.
- **Subscriber impl** — there is no default. The Bus surface accepts
  any `Subscriber`; adopters wire Kafka, NATS, Redis Streams, or
  in-process channels.
- **EventPublisher** — Bus responses and `BusSink` publish via
  `api.EventPublisher`. Adopters bring the backend.
- **Secrets** — HMAC secrets, OAuth client_id/secret, signed-URL
  signing keys: load from env at construction time. Nothing in the
  package reads env directly.
- **Authentication wiring** — `WithRESTAuth(api.AuthFunc)` /
  `WithSSEAuth(api.AuthFunc)`. The bridge does not assume any
  identity provider.

## Common patterns

### Sink fan-out via Runner wrapper

The package does not call sinks automatically. Wrap your Runner:

```go
type sinkRunner struct {
    inner cmdsurface.Runner
    sinks cmdsurface.SinkSet
}

func (s *sinkRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
    res, err := s.inner.Run(ctx, inv)
    _ = s.sinks.Emit(ctx, inv, res, err)
    return res, err
}

func (s *sinkRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
    return s.inner.Stream(ctx, inv, out)
}

b := cmdsurface.New(root, cmdsurface.WithRunner(&sinkRunner{
    inner: cmdsurface.InProcessRunner(root),
    sinks: cmdsurface.SinkSet{
        {Sink: &cmdsurface.LogSink{}, OnOK: true, OnError: true},
        {Sink: &cmdsurface.WebhookSink{URL: os.Getenv("AUDIT_URL")},
            OnError: true, Paths: []string{"widget *"}},
    },
}))
```

See `examples/cmdsurface/sinkrunner.go`.

### Per-environment surface toggling

Switch surfaces per environment via YAML without recompiling:

```yaml
# dev.yaml
surfaces:
  defaults: [cli, lib, mcp, rest, ws, sse]
  commands:
    "*":
      enabled: [cli, lib, mcp, rest, ws, sse]

policy:
  destructive_default: allow   # dev: anything goes
```

```yaml
# prod.yaml
surfaces:
  defaults: [cli, lib, mcp]
  commands:
    "report *":
      enabled: [cli, lib, mcp, rest]
    "widget delete":
      enabled: [cli, lib]      # destructive: never remote in prod

policy:
  destructive_default: deny_remote
```

```go
cfg, _ := cmdsurface.LoadFile(os.Getenv("KIT_CONFIG"))
b, _ := cmdsurface.FromConfig(root, cfg)
```

### Migrating an existing kit-CLI to REST/MCP

The migration is additive — no cobra handler changes:

1. Build the bridge: `b := cmdsurface.New(root)`.
2. Pick the surfaces to expose: `b.Expose("read-only *",
   cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP)`.
3. Mount them: `cmdsurface.MountREST(b, r)`,
   `cmdsurface.MountMCP(b, r)`.
4. Wire `WithRESTAuth` for `kit/auth-required=true` leaves.

The cobra binary keeps working unchanged. Destructive leaves stay
unreachable on REST/MCP unless explicitly opted in via `Policy.
AllowDestructiveOn`.

## Threat model

Three primary risks the bridge defends against:

1. **Destructive remote exposure.** A `widget delete` leaf reachable
   on REST without auth = data loss. Defense:
   `kit/side-effect=destructive` blocks every remote surface unless
   the surface is listed in `Policy.AllowDestructiveOn`; YAML
   `destructive_default: deny_remote` is the conservative default.
2. **Webhook spoofing.** `POST /hooks/widget-create` from an attacker
   triggers writes. Defense: `WebhookAuth.Verify` runs before
   template execution; `AuthHMAC` (constant-time HMAC-SHA256) and
   `AuthBearer` (constant-time bearer) are built in. Mappings
   targeting auth-required leaves with `AuthNone` are refused at
   mount.
3. **Signed URL replay / privilege escalation.** A one-shot exec
   link is shared or replayed. Defense: signed URLs carry single-use
   nonces (`NonceStore.Consume`), expiry (`SignedToken.Exp`), the
   exact `Invocation` baked into the token (path + args + flags, not
   user-supplied), and a revocation list (`NonceStore.Revoke`).
   `Class.AuthRequired` and `Class.RequiresConfirmation` are skipped
   because the signed URL IS the auth; the destructive ceiling
   still applies.

Full design and surface inventory: `.tlc/tracks/cmdsurf/spec.md`.

## Status

Implemented (this package):

- Foundation: `Bridge`, `Leaf`, `Invocation` / `Result` / `Event`,
  `Runner` / `InProcessRunner`, `SafetyClass` / `Policy`, YAML
  `Config` / `LoadFile` / `FromConfig`.
- Surfaces: CLI (cobra), REST, RPC, MCP, WS, SSE, Bus, Cron, Lib,
  Webhook, OAuth callback, Signed URL.
- FaaS adapters: AWS Lambda (5 event types), Cloud Run.
- Sinks: Log, File, Webhook, Bus.

Stubbed:

- `SubprocessRunner` — constructor exists for wiring; the body
  returns "not implemented in foundation wave". Adopters who need
  process isolation supply their own `Runner` impl.

Deferred (out of scope; file as follow-up tracks if pursued):

- GraphQL surface (schema mismatch is severe; defer until requested) —
  track `cmdsurf-graphql` to be filed.
- Slack slash command / inbound email (build on Webhook) — track
  `cmdsurf-slack` to be filed.
- gRPC-raw (`.proto`) alongside ConnectRPC.
- Multi-tenant signed-URL issuance with per-tenant keys.
- OpenTelemetry context propagation through `Invocation.Meta` —
  track `cmdsurf-otel` to be filed.

## Testing and end-to-end

Every surface has a `*_test.go` covering the happy path plus
sentinel-error denial cases. The working example
`examples/cmdsurface/` exercises every surface together in one
binary; the `examples/cmdsurface-faas/` companion exercises the
Lambda + Cloud Run adapters. End-to-end tests in both directories
are gated by the `e2e` build tag — run with `go test -tags e2e
./examples/cmdsurface/...`.

Cross-references: `go/transport/cmdsurface/bridge_test.go`,
`go/transport/cmdsurface/safety_test.go`,
`go/transport/cmdsurface/runner_test.go`,
`go/transport/cmdsurface/config_test.go`,
`go/transport/cmdsurface/sink_test.go`,
`examples/cmdsurface/main.go`,
`examples/cmdsurface/setup.go`,
`examples/cmdsurface/sinkrunner.go`,
`.tlc/tracks/cmdsurf/spec.md`.
