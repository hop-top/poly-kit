# mcp-tasks

## What it answers

How an MCP server built on the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) serves the
**tasks extension** (`io.modelcontextprotocol/tasks`,
[SEP-2663](https://modelcontextprotocol.io/seps/2663-tasks-extension)):
durable, pollable long-running tool calls with cooperative
cancellation, input requests, TTLs, and per-principal task isolation.
It is a standalone module, independent of kit; `go/transport/mcpsdk`
binds it to a kit command tree via `WithTasks`.

> The tasks extension is **experimental**, and so is this module. It is
> pinned to the
> [ext-tasks](https://github.com/modelcontextprotocol/ext-tasks) draft
> schema at revision
> [`2c1425d`](https://github.com/modelcontextprotocol/ext-tasks/commit/2c1425d9a288b9b1f489430fe1e00bb392b47e48)
> (dated 2026-07-15, fetched 2026-08-13); wire shapes follow that
> revision exactly. Breaking changes should be expected until the
> extension stabilizes.

> The module path `mcpext.example/tasks` is an interim placeholder (the
> `.example` TLD is reserved by RFC 2606, so it cannot collide with a
> real import path). The module is designed for contribution to the
> `modelcontextprotocol` organization and will be renamed when it finds
> its home.

## Use it when

- build the extension → `tasks.New(&tasks.Options{TTL: ..., PollInterval: ..., Principal: fn})`
- advertise it under `capabilities.extensions` → `tasks.DeclareServerCapability(so)`
- register `tasks/get` / `tasks/update` / `tasks/cancel` → `ext.Attach(server)`
- decide per call whether to detach → `tasks.ClientDeclares(req)`, then `ext.StartTask(ctx, req, executor)`
- ask the client for input mid-task → `Handle.RequestInput`
- update the human-readable status → `Handle.SetStatusMessage`
- share task records across processes → supply a `Store`

## Quick start

```go
ext := tasks.New(&tasks.Options{TTL: 30 * time.Minute})

so := &mcp.ServerOptions{}
tasks.DeclareServerCapability(so)   // capabilities.extensions
server := mcp.NewServer(&mcp.Implementation{Name: "example"}, so)
if err := ext.Attach(server); err != nil {
    return err
}

server.AddTool(&mcp.Tool{Name: "bake", InputSchema: schema},
    func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        if !tasks.ClientDeclares(req) {
            return bakeInline(ctx, req)
        }
        return ext.StartTask(ctx, req, bakeSlowly)
    })
```

## Contract

- Both attachment points are required. `StartTask` errors, creating nothing, until `Attach` has installed the middleware that turns its marker result into a wire `CreateTaskResult`.
- Creation is server-directed: a `CreateTaskResult` is only ever returned to clients that declared the extension for that request, and never on protocol versions before 2026-06-30.
- Durable before respond: the task is persisted before the `CreateTaskResult` is released, so an immediate `tasks/get` always resolves.
- Task IDs carry 128 bits from `crypto/rand`. Every `tasks/*` request is authorized against the creating principal; unknown, expired and foreign task IDs produce one identical error, so there is no existence oracle, and no `tasks/list` to leak across callers.
- `tasks/list` and `tasks/result` are deliberately unregistered: the SDK answers them with the `-32601` SEP-2663 mandates.
- `Mcp-Method` and `Mcp-Name` are validated per SEP-2243 from protocol version `2026-07-28` onward; a missing or disagreeing header answers `-32020` (`HeaderMismatch`). Routing is decided by the body method alone.
- Fault separation is strict: executor errors become `failed` tasks carrying the JSON-RPC error; tool-level errors (`isError: true`) become `completed` tasks carrying the error result.
- With MRTR ([SEP-2322](https://modelcontextprotocol.io/seps/2322-MRTR)), resolve every exchange synchronously **before** `StartTask`; the task phase keeps its own `inputRequests` key namespace. Everything runs inside the SDK's own handler, the extension adds no HTTP layer of its own.

## Neighbours

- `go/transport/mcpsdk`: kit's binding of this module to a `cmdsurface` Bridge, via `WithTasks`.
- `go/transport/cmdsurface`: the bridge and policy gate that binding enforces at task creation.

## See also

- [MCP tasks extension reference](../../docs/adopters/reference/mcp-tasks.md): what the module serves, the full usage walkthrough, routing headers, how it hangs off the SDK, and the limitations
- [serve-mcp-with-the-sdk.md](../../docs/adopters/guides/serve-mcp-with-the-sdk.md): the kit-side task walkthrough
- [LICENSE](LICENSE): Apache-2.0
