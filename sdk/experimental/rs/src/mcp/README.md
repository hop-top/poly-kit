# mcp

## What it answers

How does an existing command tree get exposed to MCP clients as tools across both spec eras, with
destructive calls gated behind confirmation? The command tree itself is `hop_top_kit::cli`; HTTP client
policy is `hop_top_kit::netpolicy`.

## Use it when

- a tool's leaves should be callable by an MCP client → build a `Bridge` of `Leaf`s, `Surface::mount`, route
  `POST /mcp` to `Surface::call`
- a leaf is destructive or needs confirmation → `Leaf::with_class(SafetyClass { .. })`; the default
  `Policy` blocks every remote destructive invocation
- retries may land on any instance → set `MountOptions::confirmation_key`, shared across instances
- an adopter wants the `tasks/*` extension → `tasks_enabled: true`; off, it answers `-32601`

## Quick start

```rust
use hop_top_kit::mcp::{Bridge, CallResult, HttpRequest, Leaf, MountOptions, Surface};

let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
    Ok(CallResult::ok("pong\n"))
}));
let surface = Surface::mount(bridge, MountOptions::default()).unwrap();

let request = HttpRequest::post(
    "/mcp",
    r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}"#,
);
let response = surface.call(&request);

assert_eq!(response.status, 200);
assert!(response.body_str().contains("pong"));
```

## Contract

- Feature `mcp` pulls in `rmcp` (default features off, vocabulary only), `serde`, `serde_json`; no server,
  no tokio. Authority: the crate [feature table](../../README.md#features).
- `Surface::call` is a synchronous request to response function; the crate binds no HTTP server.
- Both eras on one mount: `2024-11-05` (handshake) and `2026-07-28` (stateless `_meta` envelope), detected
  per request. `spec_versions: Some(vec![])`, a negative `cache_ttl_ms` or an empty `confirmation_key` is a
  `MountError`, not a mount that serves nothing.
- A blocked destructive call renders as an `isError` result at HTTP 200; a missing confirmation on the
  header gate is HTTP 428.
- `tools/list` re-reads the bridge per request: no caching, and `Leaf::with_flags_on_first_execution`
  reproduces cobra's lazy `--help`.
- Naming trap: `mcp::Surface` is the mounted handler; the safety enum is `mcp::SurfaceKind`.
- Parity: [mcp-wire.json](../../../../tests/cross-lang/fixtures/mcp-wire.json), replayed byte for byte,
  cases and sequences, by `tests/mcp_wire_conformance.rs`. Where prose and fixture disagree, the fixture wins.

## Neighbours

- `hop_top_kit::cli` (src/cli.rs): the clap tree; this module takes `Leaf`s, not clap commands
- `hop_top_kit::netpolicy`: outbound request policy, the other side of the network boundary
- `hop_top_kit::serve`: hosting the mounted surface as a long-running service

## See also

- [Serve MCP from any SDK](../../../../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
- [Expose your CLI over MCP](../../../../../docs/adopters/guides/expose-cli-over-mcp.md)
- [Parity README, MCP wire contract section](../../../../../contracts/parity/README.md)
- [Crate README, MCP surface](../../README.md#mcp-surface)
- Go reference: [go/transport/mcpsdk](../../../../../go/transport/mcpsdk/README.md)
