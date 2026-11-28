# Serving and transports

Open when hosting services, MCP tools, a REST or RPC server, a socket, or
choosing between transports. Long form: [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/rs-sdk.md#serve).

| Need | Feature | Notes |
|------|---------|-------|
| Service lifecycle, signals, graceful stop | `serve`; `serve-cli` for `serve [SERVICE]` | async, bring your tokio runtime |
| Commands as MCP tools | `mcp` | [snippet](../assets/snippets/mcp-tools/mcp-tools.rs); default-closed for destructive leaves |
| REST: router, middleware, OpenAPI, SSE | `api-server` | native only |
| TLS and mTLS; websocket hub; idempotency keys | `api-server-tls`, `api-server-ws`, `api-server-idempotency` | each adds to `api-server` |
| Connect, gRPC, gRPC-Web on one port | `rpc-server` | server only |
| Local 0600 unix socket, NDJSON | `socket` | unix only |
| Inbound webhooks | `webhook`; `webhook-sha1` for legacy senders | needs `api-server` and `kv` |
| Bearer, JWT and API-key auth | `authn`; `authn-rsa`, `authn-remote`, `authn-apikeys` | core is wasm-safe |
| Guarded outbound HTTP, `--offline` | `api`, `netpolicy` | `netpolicy` alone is wasm-safe |

The crate [feature table](https://github.com/hop-top/poly-kit/blob/next/sdk/rs/README.md#features)
lists what each feature pulls in with crate counts.

Gaps taken from the parity page:

- Command surface bridge: the MCP leg only; no other surface projects the tree.
- ConnectRPC: the server only; no typed client and no command-tree service.

[Coverage](coverage.md) lists the rows with no Rust module, for example
the service command group and served observability.
