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
  snapshot `SafetyClass`, a per-surface `Enabled` map, and the
  `Descriptor` it was built from.
- **Descriptor** — the canonical reflection of one command, from
  `go/ai/cmdreflect`. See [Command reflection](#command-reflection).
- **Surface** — a transport projection identified by a string constant
  (`SurfaceREST`, `SurfaceMCP`, etc.). Thirteen surfaces are declared.
- **Invocation** — the transport-agnostic call envelope: `Path`,
  `Args`, `Flags`, `Meta`. Every surface decodes its wire format into
  this shape.
- **Result** — the unified return value (`ExitCode`, `Stdout`,
  `Stderr`, optional `Data`). Surfaces map it onto their wire format.
  See [Execution](#execution) for how `Data` is populated.
- **Event** — one streaming frame (`stdout` / `stderr` / `progress` /
  `done`) produced by `Runner.Stream`.
- **Runner** — executes an `Invocation`. The default
  `InProcessRunner(root)` re-enters the cobra tree in-process, one
  invocation at a time; `InProcessRunner(nil, WithRootFactory(f))`
  builds a tree per invocation and runs them in parallel;
  `SubprocessRunner(binary)` spawns a process per invocation.
- **Sink** — fan-out target for completed invocations (log, file,
  webhook, bus). Orthogonal to surfaces.
- **Policy** — the destructive ceiling. Conservative default: no remote
  surface can invoke a `kit/side-effect=destructive` leaf unless the
  surface is listed in `Policy.AllowDestructiveOn`.
- **Mapping** — adopter-supplied binding from an external trigger
  (webhook slug, Lambda event, signed token) to a leaf, with optional
  template-driven flag extraction.

## Command reflection

The bridge does not walk the cobra tree itself. `go/ai/cmdreflect` is
kit's single reflector: `cmdreflect.Reflect(root)` returns a `Tree`
holding one `Descriptor` per command, and `New` builds the bridge's
leaves from it.

One descriptor feeds every consumer — the `<tool> spec` manifest,
OpenAPI projection, the MCP tool list, and every surface in this
package. Before it existed, each of those derived its own view of the
same tree and dropped a different subset of commands with no record of
why; a command could appear on one surface and silently vanish from
another.

A `Descriptor` carries the command's path, use/short/long text,
aliases, flags (types, defaults, required-ness, hidden and deprecated
markers), declared positional args, output schema, resolved safety
(side-effect tier, permission tokens, confirmation, exit codes), and
surface metadata (hidden, deprecated, reserved, transport
annotations).

**Nothing is dropped.** Every command in the tree gets a descriptor.
A command a surface must not expose is still described, with
`Invocable` false and a `NonInvocableReason` naming the rule:

| Reason | Meaning |
|--------|---------|
| `not-runnable` | a command group: has subcommands, no action of its own |
| `builtin` | cobra/fang framework command (`help`, `completion`, `man`, `__complete`) |
| `hidden-internal` | `Hidden` is set: not part of the supported surface |
| `deprecated` | carries a deprecation marker; withheld from projected surfaces |
| `interactive` | `kit/side-effect=interactive`: needs a terminal and a human |
| `unauthorized-destructive` | destructive and not authorized on this surface |
| `management-only` | reserved to the tool's own management surface (e.g. `spec`) |
| `self-hosting` | `serve` and its children, `kit/network=ingress`, or `kit/self-hosting`: runs from the CLI only |
| `malformed-schema` | declared metadata does not resolve (bad side-effect value, invalid output schema) |

Exactly one reason is recorded per command; when several rules apply
the most specific wins, so the answer to "why can't I call this?" does
not depend on walk order.

`Bridge.Leaves()` returns the invocable commands.
`Bridge.NonInvocable()` returns the rest with their reasons, and
`Bridge.Descriptors()` returns both — use those to build a capability
endpoint that advertises the whole surface rather than only the
callable part.

The bridge reflects with `AllowInteractive` and `AllowReserved`, so
interactive and management-only commands are leaves — describable,
and withheld per surface — and `Invoke` refuses an interactive leaf
with `ErrNotInvocable` before the destructive ceiling, whatever the
surface. Nothing lifts `self-hosting`: those commands are never
leaves, and a call to one is an unknown command. The runner refuses
both again as a backstop — see [Execution](#execution).

`Classify(cmd)` still works and is unchanged in behavior, but it
reflects one command in isolation. Prefer `Leaf.Descriptor`.

## Execution

The runner is where an invocation becomes a command execution. The
normative rules are in the
[serve-lifecycle contract](../../../docs/contracts/serve-lifecycle.md#execution);
this is the package view.

### Runners

```go
func InProcessRunner(root *cobra.Command, opts ...RunnerOption) Runner
func WithRootFactory(newRoot func() *cobra.Command) RunnerOption
func SubprocessRunner(binaryPath string) Runner
```

| Runner | Isolation | Concurrency |
|--------|-----------|-------------|
| `InProcessRunner(root)` | one shared tree; flag chain reset to its baseline around every invocation, leaf context set explicitly, empty stdin, writers and argv restored | serialized: one invocation at a time |
| `InProcessRunner(nil, WithRootFactory(f))` | a fresh tree per invocation; nothing shared, nothing reset | parallel |
| `SubprocessRunner(binary)` | a process per invocation; cancellation kills the process group | parallel |

The **baseline** the shared runner resets to is the flag state at
construction. `cmdsurface.New(root)` builds the runner when the
bridge is built — at service start for a kit root — so the
operator's own command line (`--no-color`, `-c key=val`) is what every
served invocation starts from, plus only the flags it carries.

A root factory must return a tree sharing no mutable state with the
ones before it: no flag bound to a package-level variable, no
closure over a shared struct. A tree from `cli.New` is gated inside
`Root.Execute`, so a factory over `cli.New` runs ungated; a kit root
uses the shared form until cli exposes a prepare-without-execute
hook.

What no runner isolates: process-wide effects of the command's own
code or the tree's hooks — the working directory, the environment,
package-level variables, `cobra.OnInitialize` state.

### Structured output

`Result.Data` is populated by decoding, never by scraping text:

| Command declares a schema | Invocation `flags.format` | `Stdout` | `Data` |
|---------------------------|---------------------------|----------|--------|
| yes | absent | empty | decoded from the command's `--format=json` output |
| yes | `json` | the JSON text | decoded |
| yes | any other | that rendering | nil |
| no  | anything | as the command produced it | nil |

Decoding requires standard output to be exactly one JSON document;
numbers arrive as `json.Number`, so a transport re-encodes the digits
the command wrote. A command that writes text after its document
leaves `Data` nil and the streams intact.

### Cancellation

The invocation's context is the command's `cmd.Context()`. In
process, cancellation is cooperative — a command that never reads its
context runs to completion. In a subprocess, the child's process
group is killed (Unix) or the child itself (Windows). A command that
fails while the context is done is reported through the runner's
error, wrapping `context.Canceled` or `context.DeadlineExceeded`,
alongside the partial `Result`; under `Stream` the `done` event is
delivered first.

### Refusals

`ErrNotInvocable` is returned for a leaf that can never execute
through a transport: an interactive command (no terminal here) or a
self-hosting one (the runner is the process it would start a server
inside of, or replace). `Bridge.Invoke` returns it first, before the
destructive ceiling and the permission gate, so every surface is
covered; the in-process runner returns it again as a backstop for
callers that reach a runner directly. The message names the
reflector's reason. `SubprocessRunner` holds no tree and cannot
classify; discovery and the bridge withhold both classes before it is
reached.

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

Wire shape: MCP JSON-RPC 2.0 at the configured path (default `/mcp`),
serving **both protocol revisions from the one mount**:

- **2024-11-05** — `initialize`, `tools/list`, `tools/call`; plain
  JSON-RPC bodies, no required headers.
- **2026-07-28** — stateless per-request `_meta` (reserved
  `io.modelcontextprotocol/*` keys), `server/discover`, header
  routing with `MCP-Protocol-Version` / `Mcp-Method` / `Mcp-Name`
  header-body validation (`-32020` on mismatch), `resultType` +
  serverInfo-stamped result envelopes, `ttlMs` / `cacheScope` cache
  hints on list results, and GET / DELETE answered with 405.

Each POST routes to exactly one revision's handler by per-request
detection: `initialize` is always legacy; a modern marker
(`Mcp-Method` / `Mcp-Name` header, the reserved `_meta`
protocolVersion key, or `method: "server/discover"`) routes modern;
everything else takes the legacy path byte-for-byte unchanged. Full
precedence rules: `docs/adr/0004-mcp-dual-spec-surface.md`.

Tool name is the dotted leaf path (e.g. `widget.add`). Flag schema is
derived from the leaf's pflag set; `Result.Stdout` becomes a text
content block, non-zero `ExitCode` sets `isError: true`. The modern
path additionally emits `Result.Data` as `structuredContent`.

Options:

- `WithMCPPath(path string)` — override `/mcp`.
- `WithMCPServerInfo(name, version string)` — identity returned by
  `initialize`, reported by `server/discover`, and stamped into every
  modern result's `_meta` serverInfo.
- `WithMCPSpecVersions(versions ...MCPSpecVersion)` — pin the enabled
  revision set (`MCPSpec20241105`, `MCPSpec20260728`); absent = both.
  An empty call or an unrecognized version fails the mount.
- `WithMCPCacheHints(ttl time.Duration, scope MCPCacheScope)` — set
  `ttlMs` / `cacheScope` on modern `server/discover` and `tools/list`
  results; absent = `0` / `"private"`. A negative ttl or unknown
  scope fails the mount.
- `WithMCPOriginAllowlist(origins ...string)` — exact-match `Origin`
  validation on the modern path (403 on mismatch); absent = no check.
  Configure it (or bind to localhost) on any routable deployment.
- `WithMCPConfirmationKey(key []byte)` — enable the spec 2026-07-28
  MRTR confirmation flow for `kit/requires-confirmation` leaves on
  the modern path: clients declaring the `elicitation` capability get
  a `resultType: "input_required"` round-trip with an HMAC-SHA-256
  protected `requestState` instead of the `X-Confirm-Token` header
  gate (which remains for everyone else). Key must be non-empty and
  shared across instances; mount fails on an empty key.

The declarative `mcp:` config block (`MCPConfig`) mirrors these
options field-for-field; `FromConfig` parses it but does not mount —
adopters translate it to options themselves, like the webhook / bus /
cron blocks.

Protocol reference: <https://modelcontextprotocol.io/specification>.
Adopter walkthrough: `docs/adopters/guides/expose-cli-over-mcp.md`.
See `go/transport/cmdsurface/surface_mcp_test.go`,
`surface_mcp_dispatch_test.go`, `surface_mcp_modern_test.go`, and
`surface_mcp_modern_confirm_test.go`.

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

RFC 9207 issuer validation: set `OAuthProvider.ExpectedIssuer` to
the provider's issuer identifier (absolute URL, no query/fragment —
checked at mount) and the callback requires an `iss` query parameter
equal to it (simple string comparison) on every response, error
responses included, rejecting with `missing_iss` / `issuer_mismatch`
before provider-error handling and state consumption. The validated
issuer rides `Meta.Extra["oauth_issuer"]`; map `"iss"` in
`FlagFromQuery` to hand it to the leaf. Unset = check disabled. See
`go/transport/cmdsurface/surface_oauth_iss_test.go`.

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
surface, path pattern, success/error).

Two paths reach a sink:

- **Bridge-registered sinks** (`WithSinks`, or the telemetry sink
  `FromConfig` adds). `Bridge.Invoke` emits to them for every refusal
  — unknown command, surface not enabled, the destructive ceiling,
  the permission gate — and for every execution on a remote surface
  (every surface but `cli` and `lib`). A transport reports its own
  pre-bridge refusals through `Bridge.Audit`, with
  `ErrAuthRefused` for a failed authentication, so one stream carries
  every verdict with the same `Meta`. The modern MCP
  confirmation-state rejection is emitted here too.
- **Runner-wrapping sinks** (the `sinkRunner` pattern below) observe
  only invocations that reach the `Runner`, on every surface including
  the local ones. They are the adopter's own path and are unaffected.

An audit record's verdict is `err` when the call was refused before
running and `res.ExitCode` when it ran; a confirmation refusal is the
latter, because confirmation is the command's own gate.

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
- Working wiring example:
  `hops/main/examples/cmdsurface/telemetry.go`

## Safety matrix

How cobra annotations gate each surface:

| Annotation                     | CLI / Lib | REST / SSE / WS    | RPC                | MCP   | Webhook              | OAuth     | Signed              | Bus                   | Cron               | FaaS                 |
|--------------------------------|-----------|--------------------|--------------------|-------|----------------------|-----------|---------------------|-----------------------|--------------------|----------------------|
| `kit/side-effect=destructive`  | allowed   | `Policy.AllowDestructiveOn` | same | same  | same                 | same      | same                | same                  | same               | same                 |
| `kit/auth-required=true`       | n/a       | `api.Auth(authFn)` (deny-all if unset) | `Authorization` header or `Meta.Caller` | n/a | `WebhookAuth.Verify` is the gate; `AuthNone` is refused | state IS auth | signed URL IS auth | `headers.authorization` or `Meta.Caller` | refused unless `WithCronAllowAuth(true)` | IAM is the gate |
| `kit/requires-confirmation=true` | n/a     | `X-Confirm-Token` header (428 when missing) | same | n/a | refused unless `WithWebhookAllowConfirmation()` | refused at mount | skipped | `headers.x-confirm-token` | (cron has no confirm channel — refused if also auth-required without opt-in) | refused at mount |
| `kit/permissions=<csv>`        | `PermissionFunc` | `PermissionFunc` | same | same | same | same | same | same | same | same |

`kit/permissions` is parsed into `Leaf.Class.Permissions` and enforced
by the bridge's `PermissionFunc` (`WithPermission`), which runs after
the destructive ceiling and before the Runner on every surface: the
adopter's decision, kit's gate. The default permits everything.

The mappings of bridge sentinel errors to wire format are uniform:

| Bridge sentinel        | REST / SSE        | RPC               | WS / Bus / Webhook / Signed | OAuth        | Lambda            |
|------------------------|-------------------|-------------------|------------------------------|--------------|-------------------|
| `ErrUnknownCommand`    | 404 `unknown_command` | `CodeNotFound`  | `unknown_command`            | 500 (mount-time refusal) | event-type response |
| `ErrSurfaceNotEnabled` | 404 `not_enabled` | `CodeNotFound`    | `not_enabled`                | (mount-time refusal)     | (mount-time refusal) |
| `ErrDestructiveBlocked`| 403 `destructive_blocked` | `CodePermissionDenied` | `destructive_blocked` | 403 / (mount-time refusal) | (mount-time refusal) |
| `ErrNotInvocable`      | 404 `not_invocable` (withheld at mount) | `NOT_INVOCABLE` (socket) | passthrough | (mount-time refusal) | (mount-time refusal) |
| `ErrPermissionDenied`  | 403 `permission_denied` (projection) | `DENIED` (socket) | passthrough | passthrough | passthrough |

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

The surface matrix above is the full surface inventory.

## Status

Implemented (this package):

- Foundation: `Bridge`, `Leaf`, `Invocation` / `Result` / `Event`,
  `Runner` / `InProcessRunner`, `SafetyClass` / `Policy`, YAML
  `Config` / `LoadFile` / `FromConfig`.
- Surfaces: CLI (cobra), REST, RPC, MCP, WS, SSE, Bus, Cron, Lib,
  Webhook, OAuth callback, Signed URL.
- FaaS adapters: AWS Lambda (5 event types), Cloud Run.
- Sinks: Log, File, Webhook, Bus.

Runners: `InProcessRunner` (shared tree, serialized, isolated per
invocation), `InProcessRunner` with `WithRootFactory` (tree per
invocation, parallel), `SubprocessRunner` (process per invocation,
process-group cancellation on Unix). See [Execution](#execution).

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
`examples/cmdsurface/sinkrunner.go`.
