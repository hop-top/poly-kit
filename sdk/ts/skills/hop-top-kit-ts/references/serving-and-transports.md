# Serving and transports

Open when running a service lifecycle, serving a command tree over MCP, or calling REST or
ConnectRPC from TypeScript. Status per capability: [coverage](coverage.md). Depth:
[TypeScript API reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/ts-api-reference.md)
(sections Serve and MCP surface).

## This port ships no server

kit-ts supervises services and mounts MCP as a plain handler; it opens no HTTP or socket
listener. Your service's `start` binds the port. The REST projection of a command tree, the Unix
socket service and served authentication are Go-only (coverage lists them as lacking).

## Modules

| Import | Use for | Cue |
|--------|---------|-----|
| `@hop-top/kit/serve` | `<tool> serve [service]` lifecycle | `ServiceRegistry`, `registerServe(program, { registry, configs })` |
| `@hop-top/kit/mcp` | command tree as MCP tools | `commanderBridge`, `createMcpHandler`, `serveStdio`, `defaultPolicy` |
| `@hop-top/kit/api` | REST client for kit entity APIs | `APIClient`, `WSClient`; a client, not a server |
| `@hop-top/kit/rpc` | ConnectRPC client | `RPCClient` |

Matrix cell `unverified`: `@hop-top/kit/api`, `@hop-top/kit/rpc`, and served middleware (the MCP
audit and safety slice only). Check coverage, tell the user, test the calls you depend on.

## Serve

- `serve <service>` starts that service even when its `enabled` is false; a supervisor run that
  resolves to zero services exits 2. Exit codes: 0 clean, 1 failure, 2 usage, 3 unknown service,
  5 policy denied.
- This port does not resolve dotted config keys: pass the resolved blocks as `configs`.
- Contract and parity: serve lifecycle corpus is shared with Go; see the
  [serve lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md).

## MCP

- `assets/snippets/mcp/` lists tools from a one-leaf tree; its expected output is checked in.
- `commanderBridge` requires `run` (kit does not capture command output for you). Tools are named
  by the leaf path joined with `toolNameSeparator` (default `.`; use `_` for hosts that refuse dots).
- Default policy blocks every destructive leaf on remote surfaces, including an unannotated leaf
  named `delete`, `remove`, `rm`, `destroy`, `purge` or `drop`.
- One mount answers the `2024-11-05` and `2026-07-28` protocol eras per request. The protocol
  layer is `@modelcontextprotocol/server` and `@modelcontextprotocol/core` (v2). The legacy
  `@modelcontextprotocol/sdk` package cannot serve the newer era: do not add it.
- Host it on any stack: the handler is `(McpHttpRequest) => Promise<McpHttpResponse>`; stdio via
  `serveStdio` for a host that spawns the tool.
- Confirmation on the modern era: `headerConfirmationGate` or `ElicitationConfirmGate`; the
  `audit` option records every refusal.
- Guide: [serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-from-any-sdk.md).
