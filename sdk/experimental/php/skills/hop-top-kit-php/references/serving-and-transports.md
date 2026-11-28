# Serving and transports

Open when the tool hosts long-running services, exposes commands to MCP
clients or calls an HTTP API. Check the rows in [coverage](coverage.md)
first: serve lifecycle and the MCP server are the strongest PHP areas; the
REST toolkit and served middleware are less proven.

## Pick the entry point

| You need | Use |
|----------|-----|
| one `serve` verb supervising listeners or workers | implement `Service`, register in a `ServiceRegistry`, mount `ServeCommand::fromConfig()` |
| commands callable as MCP tools over HTTP | `Bridge` and `Mount`, with `RequestHandler` as a PSR request handler in your router |
| commands callable by a spawning host | `StdioServer::serve($bridge)` (newline-delimited JSON-RPC) |
| an HTTP API client that honours offline mode | `HopTop\Kit\Api` |

All of these live under `HopTop\Kit\Serve`, `HopTop\Kit\Mcp` and
`HopTop\Kit\Api`. HTTP hosting of MCP needs a PSR message implementation of
your choice; kit does not pick one.

## Decisions

- MCP is default-closed. A destructive leaf stays blocked on every remote
  surface until you pass a policy that opens that surface; annotate leaves
  with `kit/side-effect` so the gate can see them.
- A human confirmation gate is opt-in per leaf (`kit/requires-confirmation`).
- A runtime that cannot run a PHP process model (no long-lived worker) is a
  reason to run the `kit` binary as a sidecar instead of hosting services here.
- The serve exit codes and event names are a shared contract; read it before
  changing them:
  [serve lifecycle](https://github.com/hop-top/poly-kit/blob/next/docs/contracts/serve-lifecycle.md).

## Detail

`src/Serve/README.md`, `src/Mcp/README.md`, `src/Api/README.md` in the
installed package. Guides:
[serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/serve-mcp-from-any-sdk.md);
[PHP SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/php-sdk.md).
