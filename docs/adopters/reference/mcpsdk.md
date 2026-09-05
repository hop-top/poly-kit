# mcpsdk reference

Package reference for
[`go/transport/mcpsdk`](../../../go/transport/mcpsdk/README.md): entry
points, options, the comparison against the hand-rolled MCP surface,
the trust boundary, version negotiation, the tasks extension binding,
and the tradeoffs. The task walkthrough is
[serve-mcp-with-the-sdk.md](../guides/serve-mcp-with-the-sdk.md).

`mcpsdk` serves a [`cmdsurface`](cmdsurface.md) Bridge over the Model
Context Protocol using the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) for the
entire protocol layer. It is the SDK-backed alternative to the
hand-rolled MCP surface mounted by `cmdsurface.MountMCP`; both expose
the same bridge tools, gated identically for everything kit binds,
and adopters choose per mount.

## Entry points

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

| Entry point | What it returns |
|---|---|
| `mcpsdk.New(b, opts...)` | the `*Surface` handle: serve it (`Handler` / `Mount` / `ServeStdio`), reach the raw `*mcp.Server` (`Server()`), drive the live tool list (`Hide` / `Expose` / `Sync`) |
| `mcpsdk.Handler(b, opts...)` | a bare `http.Handler` to mount however you like |
| `mcpsdk.NewServer(b, opts...)` | the underlying `*mcp.Server`, for custom transports or direct `server.Connect` wiring |
| `mcpsdk.ServeStdio(ctx, b, opts...)` | serve a local client over stdio (`mycli mcp serve`-style commands) |

Options: `WithPath`, `WithServerInfo`, `WithInstructions`,
`WithStateless` (SEP-2567 sessionless mode; GET/DELETE become 405),
`WithJSONResponse`, `WithServerOptions`, `WithServerConfigurator`,
`WithToolDecorator`, `WithTasks` (SEP-2663 tasks extension, below).

## Beyond tools: the full SDK surface

kit binds the cobra tree; everything else the SDK server offers is
passed through, not wrapped. Two hooks:

- **`WithServerOptions(*mcp.ServerOptions)`**: the base options the
  SDK server is built with (shallow-copied; `WithInstructions`
  overrides its `Instructions`). This is where `PageSize`,
  `SubscribeHandler` / `UnsubscribeHandler`, `CompletionHandler`,
  `Capabilities`, `KeepAlive`, `GetSessionID` go.
- **`WithServerConfigurator(func(*mcp.Server))`**: runs against the
  built server after kit's tools are bound. Register prompts,
  resources, and resource templates with the SDK's own `AddPrompt` /
  `AddResource` / `AddResourceTemplate`; capability advertisement
  follows automatically from what you register (SDK inference:
  `prompts`, `resources` with `subscribe` when a `SubscribeHandler`
  is set, `completions` when a `CompletionHandler` is set). What you
  register here runs outside kit's gates, see
  [Safety and the trust boundary](#safety-and-the-trust-boundary).

What this buys, all SDK-served and covered by tests in the package:
`prompts/list` + `prompts/get`, `resources/list` + `resources/read`
(plus templates), `resources/subscribe` +
`notifications/resources/updated`, cursor pagination on every list
method (server `PageSize`, client iterators, no kit-side cursor logic
exists), and completions. Worked snippets are in
[the guide](../guides/serve-mcp-with-the-sdk.md#beyond-tools-the-rest-of-the-sdk).

### Live tool list (Hide / Expose / Sync)

The Surface keeps the SDK tool set in step with bridge enablement at
runtime. `s.Hide(pattern)` / `s.Expose(pattern)` flip `SurfaceMCP` on
matching leaves and reconcile; mutating the bridge directly works too,
call `s.Sync()` afterwards. Every effective change unlists or relists
tools and makes connected sessions receive
`notifications/tools/list_changed` (SDK behavior). Enablement and the
destructive policy ceiling are still re-checked on every call, so the
listing is advisory and the gate is authoritative: exposing a
policy-blocked destructive leaf lists a tool that still refuses.

### Descriptor enrichment

`WithToolDecorator(func(*cmdsurface.Leaf, *mcp.Tool))` runs per leaf
after kit fills the defaults (name, description, flag-derived input
schema, destructive hint): set `Title`, `OutputSchema`, `Icons`, or
any annotation. kit cannot derive `OutputSchema` mechanically, a
bridge `Result.Data` is untyped at mount time, so output schemas are
adopter knowledge and belong in the decorator.

### Streaming and progress

A `tools/call` carrying a progress token streams: the bridge Runner's
`Stream` runs the leaf and each output line is delivered as a
`notifications/progress` message on the requesting session while the
call is in flight; the terminal result still carries the full
captured output. Calls without a token use the synchronous path
unchanged. The boundary is precise: MCP tool results are a single
terminal message, so line-by-line delivery rides progress
notifications (SDK `NotifyProgress`), there is no partial-result
channel to stream into. The streaming path applies the same gates as
the synchronous one (destructive ceiling, auth, confirmation,
enablement), pinned by tests.

## Relationship to the hand-rolled surface

| | `cmdsurface.MountMCP` | `mcpsdk.Mount` |
|---|---|---|
| Protocol layer | ~400 lines in-repo | official MCP Go SDK |
| Extra dependencies | none | `modelcontextprotocol/go-sdk` (+5 small indirects) |
| Spec versions | 2024-11-05 only | 2024-11-05 to 2026-07-28 (negotiated) |
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
| Tasks (SEP-2663 extension) | none | poll core via `WithTasks` (experimental, below) |
| Safety gates | Bridge policy + enablement | identical for kit-bound tools (same types, same `mcp` surface key); adopter-registered features ungated |

## Safety and the trust boundary

Kit's safety contract covers **kit-bound tools invoked through the
bridge**: the tools this package registers from the cobra tree,
dispatched via `Bridge.Invoke`. For those, both implementations key
off `cmdsurface.SurfaceMCP`: a leaf enabled for `mcp` (config or
`Expose`) is exposed by whichever MCP implementation the adopter
mounts, and `Policy.AllowDestructiveOn` gates destructive leaves for
both identically. Destructive leaves are **blocked by default**;
auth-required leaves demand an `Authorization` header and
confirmation-required leaves an `X-Confirm-Token` header. Transports
without HTTP headers (stdio, in-memory) fail those gates closed.

That is where the guarantee ends. `WithServerConfigurator` and
`Server()` hand you the raw `*mcp.Server`, deliberately, per the
wrap-nothing design, so everything registered through them runs
under **your** responsibility, outside kit's gates:

- **Adopter-registered features are ungated.** Tools, prompts,
  resources, and completions you add via the configurator execute
  whatever their handlers do; kit's destructive/auth/confirmation
  checks never wrap them. Gate them yourself if they need gating.
- **Name shadowing replaces gated handlers.** The SDK's `AddTool`
  silently replaces an existing tool of the same name, with no error.
  An adopter call like `m.AddTool(&mcp.Tool{Name: "widget.delete"},
  myHandler)` swaps out kit's policy-gated binding for `myHandler`,
  which then executes ungated. Treat kit's dotted leaf names as
  reserved; never reuse them for adopter tools.
- **Direct Runner access bypasses the policy gate.** An adopter
  handler that calls `Bridge.Runner().Run` reaches leaves without
  the enablement or destructive checks. Dispatch through
  `Bridge.Invoke` from adopter code to stay gated.

None of this is reachable by a remote client on its own, it takes
adopter code registering it, but the boundary is real and worth
naming: kit gates what kit binds; what you register is yours.

## Version negotiation (as pinned by tests)

Legacy `initialize` handshake: 2024-11-05, 2025-03-26, 2025-06-18 and
2025-11-25 are echoed back verbatim; anything else (including
2026-07-28, which replaces `initialize`) falls back to 2025-11-25.
The 2026-07-28 protocol is served through its own per-request
negotiation (`_meta` version/capabilities plus `Mcp-*` framing
headers), handled entirely by the SDK.

## Tasks extension (SEP-2663), experimental

`WithTasks` enables the `io.modelcontextprotocol/tasks` extension:
durable, pollable long-running tool calls with `tasks/get` /
`tasks/update` / `tasks/cancel`, cooperative cancellation, TTLs, and
per-principal task isolation.

```go
s, err := mcpsdk.New(b, mcpsdk.WithTasks(mcpsdk.TasksConfig{
    Tools: []string{"report.generate", "backup.run"}, // task-eligible leaves
    TTL:   30 * time.Minute,                          // ttlMs (default 15m)
}))
```

The wire behavior lives in a standalone extension module
(`mcpext.example/tasks`, in-repo under `extensions/mcp-tasks/`, built
against the [ext-tasks] draft pinned at revision `2c1425d9a288`,
2026-08-13, and designed for donation to the MCP organization); this
package only binds it to the bridge. Both are **experimental**, like
the extension itself. Its full contract is in
[mcp-tasks.md](mcp-tasks.md).

[ext-tasks]: https://github.com/modelcontextprotocol/ext-tasks

How it behaves:

- **Server-directed, spec-exact.** An eligible leaf called by a
  client that declares the extension for the request (per-request
  `_meta` capability, protocol at or after 2026-06-30) answers with a
  `CreateTaskResult` and runs detached; every other call, including
  every SDK-client call today, since the Go SDK client negotiates an
  older protocol, returns its result inline exactly as before.
  Poll `tasks/get` until `completed` / `failed` / `cancelled`;
  `tasks/list` and `tasks/result` answer `-32601` per the SEP.
- **One execution path.** Detached execution dispatches through
  `Bridge.Invoke` on the existing Runner: enablement and the policy
  ceiling are re-checked at run time, stdout/stderr/exit render into
  the completed task's `result` exactly like a synchronous call
  (non-zero exit becomes an `isError` result, still `completed`; only
  kit-level faults such as a mid-flight `Hide` fail the task).
- **Safety enforced at creation.** A policy-blocked destructive leaf
  refuses before any task exists. Confirmation-required leaves accept
  `X-Confirm-Token` as on the synchronous path, or resolve
  confirmation synchronously through an MRTR elicitation exchange
  (SEP-2322) whose signed `requestState` binds leaf, principal, and
  expiry, either way strictly before the `CreateTaskResult`, as the
  SEP mandates. Nothing on the tasks surface (get/update/cancel) can
  execute, re-execute, or amplify a leaf; all of this is pinned by
  tests.
- **Principal-bound visibility.** Tasks are bound to the SHA-256 of
  the `Authorization` header; unknown, expired, and foreign task IDs
  answer one identical `-32602` (no existence oracle). Without
  authentication every caller shares the empty principal: isolation
  requires auth in front of the surface.
- **Deployment caveats.** The default store is in-memory and
  per-process: behind a load balancer, route `tasks/*` by the
  `Mcp-Name: <taskId>` header (clients must send it per SEP-2243) or
  supply a shared `Store`. Stdio serving is unaffected: stdio clients
  negotiate pre-2026 protocol versions, where the extension is not
  defined, so every call stays inline.

The `tasks/get|update|cancel` methods are served by a thin
HTTP-level handler in front of the SDK handler (wrapped inside
`Handler`/`Mount` automatically), scoped strictly to the reserved
`tasks/` method prefix: go-sdk v1.7.0 rejects unknown methods at the
transport layer before middleware runs, so no in-SDK seam exists.
Task *creation* rides entirely inside the SDK via receiving
middleware. Optional push (`notifications/tasks` over
`subscriptions/listen`) is not implemented: the SDK routes only its
own notification types onto listen streams, and shipping push would
mean hand-rolling transport machinery this surface exists to avoid.

This is the one place the surface's solely-the-SDK rule is amended:
**go-sdk v1.7.0 ships no tasks support** (its trackers are issue
[#626], labeled SEP-1686 in its ROADMAP, and the in-progress PR
[#755]), so implementing SEP-2663 beside the SDK duplicates nothing
the SDK provides. The moment that changes, the rule applies again:
`TestSDKNativeTasksCanary` pins the SDK's current rejection of
`tasks/*` against a bare SDK server and turns red when a future SDK
starts answering them, the signal to reconcile this extension with
upstream.

[#626]: https://github.com/modelcontextprotocol/go-sdk/issues/626
[#755]: https://github.com/modelcontextprotocol/go-sdk/pull/755

## Honest tradeoffs

- **Dependency weight.** The SDK and its transitive modules join the
  kit build. The hand-rolled surface costs nothing extra.
- **No HTTP status mirroring.** Gate refusals surface as `isError`
  tool results; HTTP-only probes cannot distinguish 401/428 the way
  they can against the hand-rolled surface.
- **Direct bridge mutation needs a `Sync()`.** The Surface's own
  `Hide`/`Expose` reconcile automatically, but `Bridge.Hide` called
  directly leaves the SDK listing stale until `s.Sync()` runs: the
  bridge exposes no change hook to observe. Stale listings are
  advisory only, calls fail closed either way.
- **Advertised is not callable.** Like the hand-rolled surface,
  policy-blocked destructive leaves are listed but always refuse;
  clients see the refusal in-band where a model can react to it.
- **Configurator power is configurator responsibility.** The raw
  `*mcp.Server` is the point of the pass-through design, and it can
  shadow or sidestep kit's gates from adopter code, see
  [Safety and the trust boundary](#safety-and-the-trust-boundary).
- **Two implementations, one contract.** The safety contract lives in
  the shared Bridge; the wire behavior differs in the ways listed
  above. Pick one per deployment rather than mounting both on the
  same router path.

## Related pages

- [serve-mcp-with-the-sdk.md](../guides/serve-mcp-with-the-sdk.md):
  the task walkthrough
- [expose-cli-over-mcp.md](../guides/expose-cli-over-mcp.md): the
  hand-rolled, zero-dependency MCP surface
- [mcp-tasks.md](mcp-tasks.md): the tasks extension module
- [cmdsurface reference](cmdsurface.md): the bridge, the policy gate,
  every other surface
