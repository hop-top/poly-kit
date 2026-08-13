# mcpsdk — SDK-backed MCP surface

`mcpsdk` serves a [`cmdsurface`](../cmdsurface/) Bridge over the Model
Context Protocol using the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) for the
entire protocol layer. It is the SDK-backed alternative to the
hand-rolled MCP surface mounted by `cmdsurface.MountMCP`; both expose
the same bridge tools, gated identically for everything kit binds
(see [Safety and the trust boundary](#safety-and-the-trust-boundary)),
and adopters choose per mount.

## Quick start

```go
b := cmdsurface.New(rootCmd)
r := api.NewRouter()

// Streamable HTTP at /mcp (POST/GET/DELETE).
if err := mcpsdk.Mount(b, r,
    mcpsdk.WithServerInfo("acme", version),
); err != nil {
    log.Fatal(err)
}
```

Other entry points:

- `mcpsdk.New(b, opts...)` — the `*Surface` handle: serve it
  (`Handler` / `Mount` / `ServeStdio`), reach the raw `*mcp.Server`
  (`Server()`), and drive the live tool list (`Hide` / `Expose` /
  `Sync`, below).
- `mcpsdk.Handler(b, opts...)` — a bare `http.Handler` to mount
  however you like.
- `mcpsdk.NewServer(b, opts...)` — the underlying `*mcp.Server`, for
  custom transports or direct `server.Connect` wiring.
- `mcpsdk.ServeStdio(ctx, b, opts...)` — serve a local client over
  stdio (`mycli mcp serve`-style commands).

Options: `WithPath`, `WithServerInfo`, `WithInstructions`,
`WithStateless` (SEP-2567 sessionless mode; GET/DELETE become 405),
`WithJSONResponse`, `WithServerOptions`, `WithServerConfigurator`,
`WithToolDecorator`.

## Beyond tools: the full SDK surface

kit binds the cobra tree; everything else the SDK server offers is
passed through, not wrapped. Two hooks:

- **`WithServerOptions(*mcp.ServerOptions)`** — the base options the
  SDK server is built with (shallow-copied; `WithInstructions`
  overrides its `Instructions`). This is where `PageSize`,
  `SubscribeHandler` / `UnsubscribeHandler`, `CompletionHandler`,
  `Capabilities`, `KeepAlive`, `GetSessionID`, … go.
- **`WithServerConfigurator(func(*mcp.Server))`** — runs against the
  built server after kit's tools are bound. Register prompts,
  resources, and resource templates with the SDK's own `AddPrompt` /
  `AddResource` / `AddResourceTemplate`; capability advertisement
  follows automatically from what you register (SDK inference —
  `prompts`, `resources` with `subscribe` when a `SubscribeHandler`
  is set, `completions` when a `CompletionHandler` is set). What you
  register here runs outside kit's gates — see
  [Safety and the trust boundary](#safety-and-the-trust-boundary).

```go
s, err := mcpsdk.New(b,
    mcpsdk.WithServerOptions(&mcp.ServerOptions{
        PageSize: 50,
        SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
            return track.Subscribe(req.Params.URI)
        },
    }),
    mcpsdk.WithServerConfigurator(func(m *mcp.Server) {
        m.AddPrompt(&mcp.Prompt{Name: "runbook"}, getRunbook)
        m.AddResource(&mcp.Resource{
            URI: "acme://state", Name: "state", MIMEType: "application/json",
        }, readState)
    }),
)
if err != nil { log.Fatal(err) }
if err := s.Mount(r); err != nil { log.Fatal(err) }

// Later: notify subscribers through the SDK.
_ = s.Server().ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: "acme://state"})
```

What this buys, all SDK-served and covered by tests here:
`prompts/list` + `prompts/get`, `resources/list` + `resources/read`
(+ templates), `resources/subscribe` + `notifications/resources/
updated`, cursor pagination on every list method (server `PageSize`,
client iterators — no kit-side cursor logic exists), and completions.

### Live tool list (Hide / Expose / Sync)

The Surface keeps the SDK tool set in step with bridge enablement at
runtime. `s.Hide(pattern)` / `s.Expose(pattern)` flip `SurfaceMCP` on
matching leaves and reconcile; mutating the bridge directly works too
— call `s.Sync()` afterwards. Every effective change unlists/relists
tools and makes connected sessions receive
`notifications/tools/list_changed` (SDK behavior). Enablement and the
destructive policy ceiling are still re-checked on every call, so the
listing is advisory and the gate is authoritative — exposing a
policy-blocked destructive leaf lists a tool that still refuses.

### Descriptor enrichment

`WithToolDecorator(func(*cmdsurface.Leaf, *mcp.Tool))` runs per leaf
after kit fills the defaults (name, description, flag-derived input
schema, destructive hint): set `Title`, `OutputSchema`, `Icons`, or
any annotation. kit cannot derive `OutputSchema` mechanically — a
bridge `Result.Data` is untyped at mount time — so output schemas are
adopter knowledge and belong in the decorator.

### Streaming and progress

A `tools/call` carrying a progress token streams: the bridge Runner's
`Stream` runs the leaf and each output line is delivered as a
`notifications/progress` message on the requesting session while the
call is in flight; the terminal result still carries the full
captured output. Calls without a token use the synchronous path
unchanged. The boundary is precise: MCP tool results are a single
terminal message, so line-by-line delivery rides progress
notifications (SDK `NotifyProgress`) — there is no partial-result
channel to stream into. The streaming path applies the same gates as
the synchronous one (destructive ceiling, auth, confirmation,
enablement), pinned by tests.

## Relationship to the hand-rolled surface

| | `cmdsurface.MountMCP` | `mcpsdk.Mount` |
|---|---|---|
| Protocol layer | ~400 lines in-repo | official MCP Go SDK |
| Extra dependencies | none | `modelcontextprotocol/go-sdk` (+5 small indirects) |
| Spec versions | 2024-11-05 only | 2024-11-05 … 2026-07-28 (negotiated) |
| Transport | single-POST JSON-RPC | streamable HTTP: sessions, SSE streams, stateless mode; stdio; any SDK transport |
| Sessions / resumption / keep-alive | none | SDK-managed |
| Auth/confirm block response | JSON-RPC result **and** mirrored HTTP status (401 / 428) | `isError` result only (HTTP status belongs to the SDK) |
| Tool identity | dotted leaf path (`widget.add`) | identical |
| Input schema | derived from cobra flags | identical derivation |
| Structured output | extra text block | native `structuredContent` |
| Prompts / resources / templates | none | full, via SDK pass-through (`WithServerConfigurator`) |
| Subscriptions + list/updated notifications | none | SDK-served (`SubscribeHandler`, `ResourceUpdated`, `tools/list_changed`) |
| Pagination | none (single response) | SDK cursor pagination on every list method |
| Progress streaming | none | per-line `notifications/progress` when the call carries a progress token |
| Runtime tool list | re-read per `tools/list` | live: `Hide`/`Expose`/`Sync` with `tools/list_changed` |
| Safety gates | Bridge policy + enablement | identical for kit-bound tools (same types, same `mcp` surface key); adopter-registered features ungated |

## Safety and the trust boundary

Kit's safety contract covers **kit-bound tools invoked through the
bridge** — the tools this package registers from the cobra tree,
dispatched via `Bridge.Invoke`. For those, both implementations key
off `cmdsurface.SurfaceMCP`: a leaf enabled for `mcp` (config or
`Expose`) is exposed by whichever MCP implementation the adopter
mounts, and `Policy.AllowDestructiveOn` gates destructive leaves for
both identically. Destructive leaves are **blocked by default**;
auth-required leaves demand an `Authorization` header and
confirmation-required leaves an `X-Confirm-Token` header. Transports
without HTTP headers (stdio, in-memory) fail those gates closed.

That is where the guarantee ends. `WithServerConfigurator` and
`Server()` hand you the raw `*mcp.Server` — deliberately, per the
wrap-nothing design — so everything registered through them runs
under **your** responsibility, outside kit's gates:

- **Adopter-registered features are ungated.** Tools, prompts,
  resources, and completions you add via the configurator execute
  whatever their handlers do; kit's destructive/auth/confirmation
  checks never wrap them. Gate them yourself if they need gating.
- **Name shadowing replaces gated handlers.** The SDK's `AddTool`
  silently replaces an existing tool of the same name — no error. An
  adopter call like `m.AddTool(&mcp.Tool{Name: "widget.delete"},
  myHandler)` swaps out kit's policy-gated binding for `myHandler`,
  which then executes ungated. Treat kit's dotted leaf names as
  reserved; never reuse them for adopter tools.
- **Direct Runner access bypasses the policy gate.** An adopter
  handler that calls `Bridge.Runner().Run` reaches leaves without
  the enablement or destructive checks. Dispatch through
  `Bridge.Invoke` from adopter code to stay gated.

None of this is reachable by a remote client on its own — it takes
adopter code registering it — but the boundary is real and worth
naming: kit gates what kit binds; what you register is yours.

## Version negotiation (as pinned by tests)

Legacy `initialize` handshake: 2024-11-05, 2025-03-26, 2025-06-18 and
2025-11-25 are echoed back verbatim; anything else (including
2026-07-28, which replaces `initialize`) falls back to 2025-11-25.
The 2026-07-28 protocol is served through its own per-request
negotiation (`_meta` version/capabilities plus `Mcp-*` framing
headers), handled entirely by the SDK.

## Tasks extension: not yet available

The `io.modelcontextprotocol/tasks` extension (durable long-running
calls with `tasks/get` / `tasks/list` / `tasks/cancel` /
`tasks/result`; governed spec-side by the merged **SEP-2663**) is
**not implemented by the SDK as of v1.7.0**. The SDK's own live
trackers are issue [#626] (labeled SEP-1686, cited in its ROADMAP)
and the in-progress PR [#755]; PR #755 shapes tasks as per-tool
opt-in, so a future SDK may recognize `tasks/*` yet reject calls for
unconfigured tools. `TestTasksMethodsUnsupported` therefore pins
today's exact rejection (HTTP 400 with an "unsupported" body): any
change in how the SDK answers `tasks/*` — full support or
recognize-but-unconfigured — turns that test red and forces a
revisit.

[#626]: https://github.com/modelcontextprotocol/go-sdk/issues/626
[#755]: https://github.com/modelcontextprotocol/go-sdk/pull/755

This package deliberately does not hand-roll the extension: the whole
point of the surface is that protocol behavior comes from the SDK.
When the SDK ships tasks support, the planned integration is a mount
option naming task-capable leaf paths (validated at mount), detached
execution on the bridge Runner with captured output, and safety
enforced at task creation (a policy-blocked destructive leaf stays
blocked; confirmation applies to the spawning call and can never be
bypassed through the tasks surface).

## Honest tradeoffs

- **Dependency weight.** The SDK and its transitive modules join the
  kit build. The hand-rolled surface costs nothing extra.
- **No HTTP status mirroring.** Gate refusals surface as `isError`
  tool results; HTTP-only probes cannot distinguish 401/428 the way
  they can against the hand-rolled surface.
- **Direct bridge mutation needs a `Sync()`.** The Surface's own
  `Hide`/`Expose` reconcile automatically, but `Bridge.Hide` called
  directly leaves the SDK listing stale until `s.Sync()` runs — the
  bridge exposes no change hook to observe. Stale listings are
  advisory only: calls fail closed either way.
- **Advertised ≠ callable.** Like the hand-rolled surface, policy-
  blocked destructive leaves are listed but always refuse; clients
  see the refusal in-band where a model can react to it.
- **Configurator power is configurator responsibility.** The raw
  `*mcp.Server` is the point of the pass-through design, and it can
  shadow or sidestep kit's gates from adopter code — see
  [Safety and the trust boundary](#safety-and-the-trust-boundary).
- **Two implementations, one contract.** The safety contract lives in
  the shared Bridge; the wire behavior differs in the ways listed
  above. Pick one per deployment rather than mounting both on the
  same router path.
