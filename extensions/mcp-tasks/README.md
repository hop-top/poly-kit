# MCP Tasks Extension for Go

This module implements the server side of the **MCP tasks extension**
(`io.modelcontextprotocol/tasks`,
[SEP-2663](https://modelcontextprotocol.io/seps/2663-tasks-extension))
for servers built on the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk): durable,
pollable long-running tool calls with cooperative cancellation,
input requests, TTLs, and per-principal task isolation.

> [!WARNING]
> The tasks extension is **experimental**, and so is this module. It
> is pinned to the
> [ext-tasks](https://github.com/modelcontextprotocol/ext-tasks) draft
> schema at revision
> [`2c1425d`](https://github.com/modelcontextprotocol/ext-tasks/commit/2c1425d9a288b9b1f489430fe1e00bb392b47e48)
> (dated 2026-07-15, fetched 2026-08-13); wire shapes follow that
> revision exactly.
> Breaking changes should be expected until the extension stabilizes.

> [!NOTE]
> The module path `mcpext.example/tasks` is an interim placeholder
> (the `.example` TLD is reserved by RFC 2606, so it cannot collide
> with a real import path). The module is designed for contribution to
> the `modelcontextprotocol` organization and will be renamed when it
> finds its home.

## What you get

- `tasks/get`, `tasks/update`, `tasks/cancel` — the extension's full
  poll-based core, served spec-exactly: `resultType` discriminators,
  empty acks, `-32601` for the reserved-but-nonexistent `tasks/list`
  and `tasks/result`, `-32003` (Missing Required Client Capability)
  with the required extension in error data, `-32602` for unknown
  task IDs, and SEP-2243 validation of the `Mcp-Method` / `Mcp-Name`
  routing headers.
- **Server-directed creation** on `tools/call`: your tool handler
  decides per call; `CreateTaskResult` is only ever returned to
  clients that declared the extension for that request (and never on
  protocol versions before 2026-06-30, where the extension is not
  defined).
- **Durable-before-respond**: the task is persisted before the
  `CreateTaskResult` is released, so an immediate `tasks/get` always
  resolves.
- **Security posture as mandated**: task IDs carry 128 bits from
  `crypto/rand` (bearer-token strength); every `tasks/*` request is
  authorized against the principal that created the task; unknown,
  expired, and foreign task IDs produce one identical error — no
  existence oracle, and no `tasks/list` to leak across callers.
- **Input requests**: an executor can move its task to
  `input_required`; responses arrive via `tasks/update` with partial
  sets accepted, unknown/answered/superseded keys ignored, and key
  uniqueness enforced over the task lifetime.
- **Strict fault separation**: executor errors become `failed` tasks
  carrying the JSON-RPC error; tool-level errors (`isError: true`)
  become `completed` tasks carrying the error result.

## Usage

```go
ext := tasks.New(&tasks.Options{
    TTL:          30 * time.Minute,
    PollInterval: 5 * time.Second,
    Principal: func(h http.Header) string {
        // Bind tasks to the caller. Any stable derivation works;
        // hashing the credential avoids retaining it.
        sum := sha256.Sum256([]byte(h.Get("Authorization")))
        return hex.EncodeToString(sum[:])
    },
})

so := &mcp.ServerOptions{}
tasks.DeclareServerCapability(so)   // capabilities.extensions
server := mcp.NewServer(&mcp.Implementation{Name: "example"}, so)
ext.Attach(server)                  // CreateTaskResult middleware

server.AddTool(&mcp.Tool{Name: "bake", InputSchema: schema},
    func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        if !tasks.ClientDeclares(req) {
            return bakeInline(ctx, req) // non-declaring clients: inline
        }
        return ext.StartTask(ctx, req, func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
            return bakeSlowly(ctx, h) // detached; ctx cancelled by tasks/cancel
        })
    })

sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
http.Handle("/mcp", ext.Handler(sdk)) // tasks/* in front, everything else untouched
```

Three attachment points, all required:

| Call | What it does |
|---|---|
| `tasks.DeclareServerCapability(so)` | declares `io.modelcontextprotocol/tasks` under `capabilities.extensions` (initialize and `server/discover`) |
| `ext.Attach(server)` | installs the middleware that turns `StartTask` results into wire `CreateTaskResult`s |
| `ext.Handler(next)` | serves `tasks/get` / `tasks/update` / `tasks/cancel` in front of the SDK handler |

Inside an executor, `h.RequestInput` blocks until the client answers
via `tasks/update`, `h.SetStatusMessage` updates the human-readable
status, and the context is cancelled when a client sends
`tasks/cancel` (cooperatively — finishing anyway is allowed, and the
spec's semantics are preserved either way).

### Multi round-trip composition

If a tool combines MRTR
([SEP-2322](https://modelcontextprotocol.io/seps/2322-MRTR)) with
tasks, resolve every MRTR exchange synchronously **before** calling
`StartTask`, as the SEP requires. The task phase keeps its own
`inputRequests` key namespace: reusing an MRTR-phase key inside the
task is legal and unambiguous.

### Routing headers

SEP-2243 requires clients to mirror the body's `method` into
`Mcp-Method`, and SEP-2663 requires `Mcp-Name` to carry `params.taskId`
on `tasks/get`, `tasks/update`, and `tasks/cancel`, so intermediaries
can route a poll to the instance holding the task without parsing the
body. SEP-2243 makes the matching obligation explicit for servers:

> Servers that process the request body MUST reject requests where the
> values specified in the headers do not match the values in the
> request body.

This module reads the body, so it validates both headers and answers a
missing or disagreeing one with HTTP 400 and JSON-RPC error `-32020`
(`HeaderMismatch`) — the same code and shape the Go SDK uses for the
core methods. Validation applies only from protocol version
`2026-07-28`, which introduced the headers; earlier clients (and
requests without `Mcp-Protocol-Version`) are served without them, per
the SEP's backward-compatibility allowance.

Routing is decided by the **body** method alone. A `tasks/*` body is
always handled by the extension, whatever `Mcp-Method` says — the
header can steer nothing, it can only fail validation.

## Why an HTTP-level handler?

go-sdk v1.7.0 rejects methods outside its own table at the transport
layer, before server middleware runs, so there is no in-SDK seam to
route `tasks/*` through. `Extension.Handler` therefore intercepts
strictly the `tasks/` method prefix — which SEP-2663 reserves for this
extension — and passes everything else through untouched. Task
*creation*, by contrast, rides entirely inside the SDK: a receiving
middleware swaps the tool handler's marker result for the
`CreateTaskResult`, so sessions, negotiation, and framing stay the
SDK's.

## Limitations

- **Push notifications are not implemented.** `notifications/tasks`
  over `subscriptions/listen` (with acknowledgements) is optional in
  SEP-2663. go-sdk v1.7.0 routes only its own notification types onto
  listen streams and rejects unknown notification methods on the
  sending path, so a conformant push would mean reimplementing the
  transport layer. The required poll-based core is complete; add push
  when the SDK exposes a seam for extension notifications.
- **In-memory store is per process.** Behind a load balancer, either
  route `tasks/*` requests to the instance holding the task (clients
  already send `Mcp-Name: <taskId>` for exactly that, per SEP-2243)
  or supply a shared `Store` implementation. Live executor state
  (cancellation, input delivery) stays process-local either way:
  another instance can read a shared record but not signal the
  running work.
- **No principal function means no isolation.** With `Principal`
  unset, every caller maps to the empty principal and can poll every
  task — acceptable only where the server itself is unauthenticated
  anyway. Set it whenever an `Authorization` header (or any caller
  identity) exists.
- **Client SDK support.** The Go SDK client (v1.7.0) unmarshals tool
  results into `CallToolResult`, which drops `CreateTaskResult`
  fields. Clients consuming tasks from Go currently need to speak the
  wire directly (this module's test suite shows how).

## License

Apache-2.0; see [LICENSE](LICENSE).
