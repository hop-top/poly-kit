# mcpsdk — SDK-backed MCP surface

`mcpsdk` serves a [`cmdsurface`](../cmdsurface/) Bridge over the Model
Context Protocol using the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) for the
entire protocol layer. It is the SDK-backed alternative to the
hand-rolled MCP surface mounted by `cmdsurface.MountMCP`; both expose
the same tools with the same safety posture, and adopters choose per
mount.

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

- `mcpsdk.Handler(b, opts...)` — a bare `http.Handler` to mount
  however you like.
- `mcpsdk.NewServer(b, opts...)` — the underlying `*mcp.Server`, for
  custom transports or direct `server.Connect` wiring.
- `mcpsdk.ServeStdio(ctx, b, opts...)` — serve a local client over
  stdio (`mycli mcp serve`-style commands).

Options: `WithPath`, `WithServerInfo`, `WithInstructions`,
`WithStateless` (SEP-2567 sessionless mode; GET/DELETE become 405),
`WithJSONResponse`.

## Relationship to the hand-rolled surface

| | `cmdsurface.MountMCP` | `mcpsdk.Mount` |
|---|---|---|
| Protocol layer | ~400 lines in-repo | official MCP Go SDK |
| Extra dependencies | none | `modelcontextprotocol/go-sdk` (+4 small indirects) |
| Spec versions | 2024-11-05 only | 2024-11-05 … 2026-07-28 (negotiated) |
| Transport | single-POST JSON-RPC | streamable HTTP: sessions, SSE streams, stateless mode; stdio; any SDK transport |
| Sessions / resumption / keep-alive | none | SDK-managed |
| Auth/confirm block response | JSON-RPC result **and** mirrored HTTP status (401 / 428) | `isError` result only (HTTP status belongs to the SDK) |
| Tool identity | dotted leaf path (`widget.add`) | identical |
| Input schema | derived from cobra flags | identical derivation |
| Structured output | extra text block | native `structuredContent` |
| Safety gates | Bridge policy + enablement | identical (same types, same `mcp` surface key) |

Both implementations key off `cmdsurface.SurfaceMCP`: a leaf enabled
for `mcp` (config or `Expose`) is exposed by whichever MCP
implementation the adopter mounts, and `Policy.AllowDestructiveOn`
gates destructive leaves for both identically. Destructive leaves are
**blocked by default**; auth-required leaves demand an
`Authorization` header and confirmation-required leaves an
`X-Confirm-Token` header. Transports without HTTP headers (stdio,
in-memory) fail those gates closed.

## Version negotiation (as pinned by tests)

Legacy `initialize` handshake: 2024-11-05, 2025-03-26, 2025-06-18 and
2025-11-25 are echoed back verbatim; anything else (including
2026-07-28, which replaces `initialize`) falls back to 2025-11-25.
The 2026-07-28 protocol is served through its own per-request
negotiation (`_meta` version/capabilities plus `Mcp-*` framing
headers), handled entirely by the SDK.

## Tasks extension: not yet available

The `io.modelcontextprotocol/tasks` extension (SEP-1686 — durable
long-running calls with `tasks/get` / `tasks/list` / `tasks/cancel` /
`tasks/result`) is **not implemented by the SDK as of v1.7.0**: the
SDK roadmap lists it as future experimental work, and the server
rejects `tasks/*` methods as unsupported (pinned by
`TestTasksMethodsUnsupported`, which is written to fail — loudly —
once an SDK release starts accepting them).

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
- **Tool set fixed at mount.** Leaves exposed when the server is
  built become tools. `Bridge.Hide` after mount makes a tool fail
  closed on call but does not unlist it (use
  `(*mcp.Server).RemoveTools` for live removal with `list_changed`
  notification).
- **Advertised ≠ callable.** Like the hand-rolled surface, policy-
  blocked destructive leaves are listed but always refuse; clients
  see the refusal in-band where a model can react to it.
- **Two implementations, one contract.** The safety contract lives in
  the shared Bridge; the wire behavior differs in the ways listed
  above. Pick one per deployment rather than mounting both on the
  same router path.
