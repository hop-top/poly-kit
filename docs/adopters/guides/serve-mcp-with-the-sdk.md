# Serve MCP with the official SDK

Project your cobra command tree onto the Model Context Protocol using
the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
for the entire protocol layer — and get the SDK's whole server feature
set (prompts, resources, subscriptions, pagination, completions) along
with your commands.

## Who this is for

Developers with a kit CLI who want an MCP server that speaks current
protocol versions, manages sessions, and can offer more than tools.

## Which surface?

kit has two MCP surfaces. They expose the **same tools** — one tool per
bridge leaf, dotted names (`widget.add`), input schemas derived the
same way from cobra flags — and gate them identically. They differ in
what carries the protocol underneath.

| | `cmdsurface.MountMCP` | `mcpsdk.Mount` (this guide) |
|---|---|---|
| Protocol layer | hand-rolled in kit | official MCP Go SDK |
| Extra dependencies | none | the SDK and its indirects |
| Protocol versions | 2024-11-05 only | 2024-11-05 … 2026-07-28, negotiated |
| Transport | single-POST JSON-RPC | streamable HTTP (sessions, SSE, stateless), stdio, any SDK transport |
| Gate refusals | `isError` result **and** matching HTTP status (401 / 428) | `isError` result only |
| Prompts, resources, subscriptions, pagination | none | full, via SDK pass-through |

Pick the hand-rolled surface when a zero-dependency, single-endpoint
MCP server is enough and HTTP-status-visible refusals matter — see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md). Pick this one when
you want current protocol coverage or anything beyond tools. Mount one
per deployment, not both on the same router path.

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

For a `acme mcp serve` command a local client launches:

```go
if err := mcpsdk.ServeStdio(ctx, b,
    mcpsdk.WithServerInfo("acme", version),
); err != nil {
    log.Fatal(err)
}
```

Stdio carries no HTTP headers, so leaves marked auth-required or
confirmation-required are never callable there — the header-based
gates fail closed. See [Safety](#safety-and-the-trust-boundary).

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
`inputSchema` built from that command's flags. If a command is
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

- Auth-required leaves demand an `Authorization` header;
  confirmation-required leaves an `X-Confirm-Token` header. Transports
  without HTTP headers (stdio, in-memory) fail those gates closed.
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
description, flag-derived input schema, destructive hint) and may set
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
exactly as before. kit's gates are enforced at creation, before any
task exists, and detached execution still dispatches through
`Bridge.Invoke`.

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
`Mcp-*` framing headers, handled entirely by the SDK.

## Tradeoffs

- **Dependency weight.** The SDK and its transitive modules join your
  build. The hand-rolled surface costs nothing extra.
- **No HTTP status mirroring.** Gate refusals come back as `isError`
  tool results only; an HTTP-only probe cannot tell 401 from 428 the
  way it can against the hand-rolled surface.
- **Direct bridge mutation needs `Sync()`.** See
  [Live tool list](#live-tool-list).
- **Advertised is not callable.** A policy-blocked destructive leaf is
  listed but always refuses. Clients see the refusal in-band, where a
  model can react to it.
- **Configurator power is configurator responsibility.** The raw
  `*mcp.Server` is the point of the design, and it can shadow or
  sidestep kit's gates from your own code.

## Related pages

- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the hand-rolled,
  zero-dependency MCP surface
- [`go/transport/mcpsdk/README.md`](../../../go/transport/mcpsdk/README.md)
  — full option and behavior reference for this package
- [`extensions/mcp-tasks/README.md`](../../../extensions/mcp-tasks/README.md)
  — the tasks extension module
- [claude-code-permissions.md](../integrations/claude-code-permissions.md)
  — annotation-driven permission mapping for AI harnesses
