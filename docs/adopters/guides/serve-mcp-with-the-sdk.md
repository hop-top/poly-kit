# Serve MCP with the official SDK

Project your cobra command tree onto the Model Context Protocol using
the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
for the entire protocol layer — and get the SDK's whole server feature
set (prompts, resources, subscriptions, pagination, completions) along
with your commands.

## Who this is for

Developers who want the SDK's MCP server under their own control: a
bare cobra tree with no kit root, their own router or process
lifecycle, stateless or task-enabled serving, or prompts and resources
beside the commands.

## Which surface?

kit's MCP surfaces expose the **same tools** — one tool per bridge
leaf, dotted names (`widget.add`), input schemas derived the same way
from cobra flags and declared positional arguments — and gate them
identically. They differ in who owns the server.

| | the `mcp` service (`mcpserve.With`) | `mcpsdk` (this guide) | `cmdsurface.MountMCP` (deprecated) |
|---|---|---|---|
| Starts from | a kit root (`cli.New`) | a `cmdsurface.Bridge` | a `cmdsurface.Bridge` |
| Protocol layer | official MCP Go SDK, through `mcpsdk` | official MCP Go SDK | hand-rolled in kit |
| Lifecycle | `<tool> serve mcp`, under the serve supervisor | yours | yours |
| Transport | streamable HTTP on its own listener, or stdio | streamable HTTP (sessions, SSE, stateless), stdio, any SDK transport | single-POST JSON-RPC |
| Protocol versions | 2024-11-05 … 2025-11-25 in a session, 2026-07-28 statelessly, on one HTTP endpoint; all over stdio | 2024-11-05 … 2025-11-25 in a session, 2026-07-28 statelessly, on one endpoint; all statelessly with `WithStateless()`; all over stdio | 2024-11-05 and 2026-07-28 on one path |
| `kit/auth-required` | verified by `Config.Auth` over HTTP; spawn trust over stdio | refused by default; established by `WithCallMeta` or `WithAuthenticated` | verified by an `api.Auth` on the router |
| Gate refusals | `isError` result | `isError` result | `isError` result **and** HTTP 401 / 428 |
| Prompts, resources, subscriptions, pagination | `Config.ServerOptions` | full, via SDK pass-through | none |
| Extra dependencies | the SDK | the SDK | none |

On a kit root, use the service — see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md). It is this package
wired into the serve lifecycle, and every option below reaches it
through `mcpserve.Config.ServerOptions`. Use `mcpsdk` directly when you
have no kit root or need the server in your own hands. `MountMCP` is
deprecated; the
[migration table](expose-cli-over-mcp.md#move-off-mountmcp) maps its
options. Mount one surface per path, never two.

## Before you begin

You need:

- A kit CLI with a cobra tree (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/transport/cmdsurface` and
  `hop.top/kit/go/transport/mcpsdk` importable
- Safety annotations on your commands — `kit/side-effect`,
  `kit/auth-required`, `kit/requires-confirmation`. The gates below
  read them; unannotated commands classify as read-only.

## Recommended path

Build a `cmdsurface.Bridge` from your root command, mount the SDK
surface on your router, and let default enablement decide which leaves
become tools. Everything else — prompts, resources, tool titles,
background tasks — is opt-in on top.

## Steps

### 1. Mount the surface

```go
// cmd/acme/mcp.go
package main

import (
    "log"

    "github.com/spf13/cobra"

    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
    "hop.top/kit/go/transport/mcpsdk"
)

func serveMCP(rootCmd *cobra.Command, version string) {
    b := cmdsurface.New(rootCmd)
    r := api.NewRouter()

    if err := mcpsdk.Mount(b, r,
        mcpsdk.WithServerInfo("acme", version),
    ); err != nil {
        log.Fatal(err)
    }
}
```

`Mount` registers the streamable HTTP handler for POST, GET and DELETE
at `/mcp` (override with `mcpsdk.WithPath`).

### 2. Know which commands became tools

The bridge walks your cobra tree and records every runnable leaf.
Under the default policy, MCP is one of the surfaces a leaf is enabled
on out of the box — so **every leaf is a tool by default**. Hidden and
deprecated commands are skipped.

Narrow it with `Expose` / `Hide` on the bridge before mounting:

```go
b := cmdsurface.New(rootCmd)
b.Hide("*", cmdsurface.SurfaceMCP)          // start closed
b.Expose("widget *", cmdsurface.SurfaceMCP) // opt leaves back in
```

Patterns are **space-separated leaf paths**, not dotted tool names:
`"widget add"` is one leaf, `"widget *"` every leaf under `widget`,
`"*"` all of them. The tool a leaf becomes is named with dots
(`widget.add`), so the two vocabularies never line up — a pattern like
`"widget.add"` matches nothing.

### 3. Serve over stdio instead (optional)

On a kit root, `acme serve mcp --stdio` already does this, with the
auth and confirmation gates adapted to a spawned process — see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md#3-connect-a-host).
On a bare bridge:

```go
if err := mcpsdk.ServeStdio(ctx, b,
    mcpsdk.WithServerInfo("acme", version),
); err != nil {
    log.Fatal(err)
}
```

When the host closes standard input, `ServeStdio` answers every request
it has already read, then returns nil. To serve streams you hold
instead of the process's own (a child's pipes, a test), connect
`s.Server()` with `mcpsdk.NewStdioTransport(in, out)`.

With the default gates, leaves marked auth-required or
confirmation-required are never callable over stdio: nothing
established the caller, and stdio carries no confirmation header.
`WithCallMeta` (returning a `Meta` with `Established:
cmdsurface.EstablishedTransport` for the spawning peer) or
`WithAuthenticated`, and `WithConfirmationElicitation`, open them. See
[Safety](#safety-and-the-trust-boundary).

## Verify the result

Point any MCP client at `http://localhost:<port>/mcp`, or probe
directly:

```bash
curl -s http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Expect one entry per enabled leaf, with `name` as the dotted path and
`inputSchema` built from that command's flags and declared
positional arguments (an `args` array; see
[positional arguments](expose-cli-over-mcp.md#positional-arguments)).
If a command is
missing, it is either hidden/deprecated in cobra, not runnable, or
disabled for `SurfaceMCP`.

## Beyond tools: the rest of the SDK

kit binds the cobra tree. Everything else the SDK server offers is
passed through rather than wrapped, through two options:

- **`WithServerOptions(*mcp.ServerOptions)`** — the base options the
  SDK server is built with. `PageSize`, `SubscribeHandler` /
  `UnsubscribeHandler`, `CompletionHandler`, `Capabilities`,
  `KeepAlive`, `GetSessionID` all live here. kit shallow-copies the
  struct and, when `WithInstructions` is also given, overrides only
  `Instructions`; every other field reaches `mcp.NewServer` untouched.
- **`WithServerConfigurator(func(*mcp.Server))`** — runs against the
  built server after kit's tools are bound. Register prompts,
  resources and templates with the SDK's own `AddPrompt` /
  `AddResource` / `AddResourceTemplate`. Repeatable; configurators run
  in registration order.

You do not declare capabilities: the SDK infers them from what you
register (`prompts` once a prompt exists, `resources` with `subscribe`
once a `SubscribeHandler` is set, and so on).

```go
s, err := mcpsdk.New(b,
    mcpsdk.WithServerInfo("acme", version),
    mcpsdk.WithServerOptions(&mcp.ServerOptions{
        PageSize: 50,
        SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
            return watch.Add(req.Params.URI)
        },
        UnsubscribeHandler: func(ctx context.Context, req *mcp.UnsubscribeRequest) error {
            return watch.Remove(req.Params.URI)
        },
    }),
    mcpsdk.WithServerConfigurator(func(m *mcp.Server) {
        m.AddPrompt(&mcp.Prompt{
            Name:        "triage",
            Description: "walk an incident triage",
            Arguments:   []*mcp.PromptArgument{{Name: "service", Required: true}},
        }, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{
                Role:    "user",
                Content: &mcp.TextContent{Text: "triage " + req.Params.Arguments["service"]},
            }}}, nil
        })

        m.AddResource(&mcp.Resource{
            URI: "acme://state", Name: "state", MIMEType: "application/json",
        }, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
            return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
                URI: req.Params.URI, MIMEType: "application/json", Text: currentState(),
            }}}, nil
        })
    }),
)
if err != nil {
    log.Fatal(err)
}
if err := s.Mount(r); err != nil {
    log.Fatal(err)
}

// Later, when the state changes, notify subscribers through the SDK.
_ = s.Server().ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{
    URI: "acme://state",
})
```

`mcpsdk.New` returns the `*Surface` handle — `Mount`, `Handler`,
`ServeStdio`, the live tool list below, and `Server()` for the raw
`*mcp.Server`. Cursor pagination on every list method comes from the
SDK's `PageSize`; kit has no cursor logic of its own.

> Everything you register here runs **outside kit's gates**. Read
> [Safety and the trust boundary](#safety-and-the-trust-boundary)
> before you register anything that acts.

## Safety and the trust boundary

kit's safety contract covers **kit-bound tools dispatched through the
bridge** — the tools this package registers from your cobra tree.
For those:

- A leaf is exposed only if it is enabled for `SurfaceMCP`.
- Destructive leaves (`kit/side-effect: destructive`,
  `destructive-local`, `destructive-shared`) are **blocked by
  default**. Opt in per surface:

  ```go
  b := cmdsurface.New(rootCmd, cmdsurface.WithPolicy(cmdsurface.Policy{
      AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP},
  }))
  ```

- Auth-required leaves run only for a caller your `WithCallMeta` or
  `WithAuthenticated` established; an `Authorization` header's
  presence is not authentication. Confirmation-required leaves need an
  `X-Confirm-Token` header. Transports without HTTP headers (stdio,
  in-memory) fail the confirmation gate closed.
- Both checks re-run on **every call**, including calls to tools that
  are currently listed. Listing is advisory; the gate is
  authoritative.

**That is where the guarantee ends.** `WithServerConfigurator` and
`Server()` hand you the raw `*mcp.Server` on purpose — kit wraps
nothing you do with it:

- **What you register is ungated.** Tools, prompts, resources and
  completions added through the configurator execute whatever their
  handlers do. kit's destructive, auth and confirmation checks never
  wrap them. Gate them yourself if they need gating.
- **Same-name `AddTool` silently replaces a gated tool.** The SDK's
  `AddTool` overwrites an existing tool of the same name without
  error. `m.AddTool(&mcp.Tool{Name: "widget.delete"}, myHandler)`
  swaps kit's policy-gated binding for `myHandler`, which then runs
  ungated. **Treat kit's dotted leaf names as reserved** and never
  reuse them.
- **Calling the Runner directly bypasses the policy gate.** A handler
  that reaches for `Bridge.Runner().Run` skips the enablement and
  destructive checks. Dispatch through `Bridge.Invoke` from adopter
  code to stay gated.

None of this is reachable by a remote client on its own — it takes
your code to register it — but the line is real: **kit gates what kit
binds; what you register is yours.**

### Browsers: Host and Origin

The MCP transport requires a server to validate `Origin`, because a
web page can otherwise drive a local server's tools through the
operator's browser. Who does it depends on where the handler is
served:

- **Mounted on kit's api service** (`APIConfig.Handlers`): the service
  checks `Host` and `Origin` for every route it serves, `/mcp`
  included, from `services.api.host_check` and
  `services.api.origin_check` (see
  [secure-remote-serving.md](secure-remote-serving.md#8-keep-browsers-out-host-origin-response-headers)).
  Add nothing here; a second check would refuse origins the service's
  configuration permits.
- **Served on a listener of your own** (`Handler`, or `Mount` on a
  router you serve yourself): give `WithOriginAllowlist`, the option
  `cmdsurface.MountMCP` spells `WithMCPOriginAllowlist`:

  ```go
  mcpsdk.Mount(b, r,
      mcpsdk.WithOriginAllowlist("https://console.example.com"),
  )
  ```

  With no arguments it admits same-origin pages only. A request with
  no `Origin` (every non-browser MCP client) or a same-origin one
  passes; a `POST` or `DELETE` from any other origin is answered
  `403` with a JSON-RPC error, `id` null, whose message starts with
  `origin_rejected`. An entry that is not `scheme://host[:port]` makes
  `New` / `Mount` return an error.

The SDK's own DNS-rebinding check applies beneath either: a request
that arrives on a loopback address with a non-loopback `Host` is
refused with `403`. Beyond loopback, check `Host` in front of the
handler with `api.HostCheck` (see the
[api reference](../reference/transport-api.md#transport-guards)).

Unlike `WithMCPOriginAllowlist`, which matches entries exactly and
checks every method, `WithOriginAllowlist` also admits same-origin
requests and leaves `GET` alone: the `GET` stream needs an
`Mcp-Session-Id` header, which a cross-origin page cannot send
without a CORS preflight the handler never grants.

## Optional

### Live tool list

The `Surface` keeps the SDK's tool set in step with bridge enablement
at runtime. Every effective change unlists or relists the tool and
makes connected sessions receive `notifications/tools/list_changed`.

```go
s.Hide("widget *")     // unlist every leaf under widget
s.Expose("widget list") // relist one of them
```

`Surface.Hide` / `Expose` reconcile automatically. Mutating the bridge
directly does not — the bridge exposes no change hook — so **call
`Sync()` yourself afterwards**:

```go
b.Hide("report generate", cmdsurface.SurfaceMCP)
s.Sync() // without this the SDK listing stays stale
```

A stale listing is advisory only: calls to a hidden leaf fail closed
either way.

### Richer tool descriptors

`WithToolDecorator` runs per leaf after kit fills the defaults (name,
description, input schema from flags and declared positional
arguments, destructive hint) and may set
or override any optional `mcp.Tool` field:

```go
mcpsdk.WithToolDecorator(func(leaf *cmdsurface.Leaf, t *mcp.Tool) {
    if t.Name == "widget.list" {
        t.Title = "List widgets"
        t.OutputSchema = map[string]any{
            "type":       "object",
            "properties": map[string]any{"widgets": map[string]any{"type": "array"}},
        }
    }
})
```

Output schemas in particular belong here: a bridge `Result.Data` is
untyped at mount time, so kit cannot derive one — it is your
knowledge, not kit's.

### Progress streaming

A `tools/call` that carries a progress token streams: the leaf runs
under the Runner's `Stream` and each output line arrives as a
`notifications/progress` message on the requesting session while the
call is in flight. The terminal result still carries the full captured
output. Calls without a token use the synchronous path unchanged, and
the streaming path applies the same gates.

### Stateless mode

`WithStateless()` serves the streamable transport without sessions: no
`Mcp-Session-Id`, a temporary session per request, and GET/DELETE
answered with 405. Use it for serverless and load-balanced deployments
with no session affinity. `WithJSONResponse()` additionally returns
`application/json` bodies instead of `text/event-stream`.

### Background tasks (experimental)

`WithTasks` enables the `io.modelcontextprotocol/tasks` extension
(SEP-2663): durable, pollable long-running tool calls with
`tasks/get` / `tasks/update` / `tasks/cancel`.

```go
s, err := mcpsdk.New(b, mcpsdk.WithTasks(mcpsdk.TasksConfig{
    Tools: []string{"report.generate"}, // task-eligible, by dotted tool name
    TTL:   30 * time.Minute,            // zero applies the 15m default
}))
```

Creation is server-directed: an eligible leaf called by a client that
declares the extension becomes a task; every other call returns inline
exactly as before. kit's gates are enforced once, at creation, before
any task exists; the detached run executes that admission, so a task
spends one rate-limit token and is audited once, when it finishes.

Both the extension and this binding are **experimental** and pinned to
a draft spec — expect breaking changes. The wire behavior, deployment
caveats (the default store is in-memory and per-process) and the full
contract live with the module: see
[`extensions/mcp-tasks/README.md`](../../../extensions/mcp-tasks/README.md).

## Version negotiation

On the legacy `initialize` handshake, protocol versions 2024-11-05,
2025-03-26, 2025-06-18 and 2025-11-25 are echoed back verbatim.
Anything else — including 2026-07-28, which replaces `initialize`
altogether, and unknown versions — falls back to 2025-11-25. The
2026-07-28 protocol is instead negotiated per request via `_meta` and
`Mcp-*` framing headers, handled entirely by the SDK, and only where
there is no session to hold: the SDK serves it from a stateless
handler, or over stdio.

Both are served on one endpoint: a client that runs `initialize` gets
a stateful session, and a 2026-07-28 request is answered statelessly,
with no session. kit routes each request to one of two SDK handlers by
the markers in
[Routing precedence](expose-cli-over-mcp.md#routing-precedence);
`WithStateless()` drops the sessions and serves every revision
statelessly.

## Tradeoffs

- **Dependency weight.** The SDK and its transitive modules join your
  build: about 2 MB on a stripped binary, 3 MB with symbols.
- **No HTTP status mirroring.** Gate refusals come back as `isError`
  tool results only; an HTTP-only probe cannot tell 401 from 428 the
  way it can against the deprecated `MountMCP`.
- **Direct bridge mutation needs `Sync()`.** See
  [Live tool list](#live-tool-list).
- **Advertised is not callable.** A policy-blocked destructive leaf is
  listed but always refuses. Clients see the refusal in-band, where a
  model can react to it.
- **Configurator power is configurator responsibility.** The raw
  `*mcp.Server` is the point of the design, and it can shadow or
  sidestep kit's gates from your own code.

## Related pages

- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the built-in `mcp`
  service on a kit root, and moving off the deprecated `MountMCP`
- [`go/transport/mcpsdk/README.md`](../../../go/transport/mcpsdk/README.md)
  — full option and behavior reference for this package
- [`extensions/mcp-tasks/README.md`](../../../extensions/mcp-tasks/README.md)
  — the tasks extension module
- [claude-code-permissions.md](../integrations/claude-code-permissions.md)
  — annotation-driven permission mapping for AI harnesses
