# mcpsdk

## What it answers

How to serve a [`cmdsurface`](../cmdsurface/README.md) Bridge over the
Model Context Protocol with the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) carrying
the entire protocol layer. It is the SDK-backed alternative to the
hand-rolled surface mounted by `cmdsurface.MountMCP`; both expose the
same bridge tools, gated identically for everything kit binds, and
adopters choose per mount. Pick `MountMCP` when the extra dependency
is unacceptable or an HTTP-only probe must see 401/428 statuses.

## Use it when

- serve streamable HTTP at a router path → `mcpsdk.Mount(b, r, ...)`
- hold the surface to drive it later → `mcpsdk.New(b, opts...)`
- mount a bare handler yourself → `mcpsdk.Handler(b, opts...)`
- reach the raw server for a custom transport → `mcpsdk.NewServer(b, opts...)`, `Surface.Server()`
- serve a local client over stdio → `mcpsdk.ServeStdio(ctx, b, opts...)`
- add prompts, resources, subscriptions, completions → `WithServerOptions`, `WithServerConfigurator`
- change the live tool list at runtime → `Surface.Hide` / `Expose` / `Sync`
- enrich a tool descriptor, notably `OutputSchema` → `WithToolDecorator`
- run long tool calls as pollable tasks → `WithTasks` (experimental)

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

## Contract

- kit gates what kit binds: leaves reach the surface only when enabled for `cmdsurface.SurfaceMCP`, destructive leaves are blocked unless `Policy.AllowDestructiveOn` names `mcp`, auth-required leaves demand an `Authorization` header and confirmation-required leaves an `X-Confirm-Token`. Transports without HTTP headers (stdio, in-memory) fail those gates closed.
- Anything registered through `WithServerConfigurator` or `Server()` runs **outside** kit's gates. The SDK's `AddTool` silently replaces a same-name tool, so kit's dotted leaf names are reserved. `Bridge.Runner().Run` bypasses the policy gate; dispatch through `Bridge.Invoke`.
- Both checks re-run on every call: the tool listing is advisory, the gate is authoritative.
- Legacy `initialize` echoes 2024-11-05, 2025-03-26, 2025-06-18 and 2025-11-25 verbatim; anything else falls back to 2025-11-25. The 2026-07-28 protocol negotiates per request, handled entirely by the SDK.
- Gate refusals surface as `isError` tool results only; there is no HTTP status mirroring.
- `WithTasks` and the extension it binds are experimental and pinned to a draft spec.

## Neighbours

- `go/transport/cmdsurface`: the bridge, the policy gate, `MountMCP` (the hand-rolled surface), every other surface.
- `extensions/mcp-tasks`: the standalone tasks-extension module `WithTasks` binds.
- `go/transport/api`: the router this surface mounts on.

## See also

- [mcpsdk reference](../../../docs/adopters/reference/mcpsdk.md): entry points, options, the comparison against the hand-rolled surface, the trust boundary, version negotiation, the tasks binding, tradeoffs
- [serve-mcp-with-the-sdk.md](../../../docs/adopters/guides/serve-mcp-with-the-sdk.md): the task walkthrough
- [expose-cli-over-mcp.md](../../../docs/adopters/guides/expose-cli-over-mcp.md): the hand-rolled, zero-dependency surface
- [MCP tasks extension reference](../../../docs/adopters/reference/mcp-tasks.md)
