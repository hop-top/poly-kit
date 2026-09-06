# mcp

## What it answers

How a Commander command tree is served over the Model Context Protocol from one mount that answers both the `2024-11-05` handshake era and the `2026-07-28` stateless era, choosing per request. Wrong module for REST or ConnectRPC surfaces (`@hop-top/kit/api`, `@hop-top/kit/rpc`) and for the service lifecycle that hosts the mount (`@hop-top/kit/serve`).

## Use it when

- expose leaves as MCP tools: `commanderBridge(root, { run })` then `createMcpHandler(bridge, opts)`
- bind to your own HTTP stack: the handler is `(McpHttpRequest) => Promise<McpHttpResponse>`; node:http, hono, express, fastify or a Worker is your call
- gate destructive tools: `policy` option, `defaultPolicy()` blocks remote destructive calls
- require confirmation on the modern era: `headerConfirmationGate` or `ElicitationConfirmGate`

## Quick start

```ts
import { Command } from 'commander';
import { createMcpHandler, commanderBridge } from '@hop-top/kit/mcp';

const root = new Command('app');
root.command('ping').description('Ping the server');
const bridge = commanderBridge(root, {
  run: async () => ({ stdout: 'pong\n', exitCode: 0 }),
});
const handler = createMcpHandler(bridge, {
  serverInfo: { name: 'app', version: '0.1.0' },
});
const res = await handler({
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' }),
});
console.log(res.status, res.body);
```

## Contract

- Wire bytes are pinned by [`sdk/tests/cross-lang/fixtures/mcp-wire.json`](../../../tests/cross-lang/fixtures/mcp-wire.json), compared as raw bytes; [`conformance.test.ts`](conformance.test.ts) replays both `cases` and `sequences`.
- Protocol layer is `@modelcontextprotocol/core` + `@modelcontextprotocol/server` (v2). The v1 `@modelcontextprotocol/sdk` cannot serve `2026-07-28` and is not a dependency.
- Era detection follows the marker set and precedence chain in [`dispatch.ts`](dispatch.ts), not the SDK's own classifier.
- `tools/list` re-reads the bridge's leaves per request; caching the leaf set fails the sequence fixture.
- `commanderBridge` requires `run`: kit does not capture a command's output for you.
- Tasks extension: `TASKS_SUPPORTED` is `false`; `discoverCapabilities` reports it.
- Parity: [`contracts/parity/README.md`](../../../../contracts/parity/README.md), section "MCP wire contract".

## Neighbours

- `@hop-top/kit/serve`: service lifecycle hosting the mount
- `@hop-top/kit/api`, `@hop-top/kit/rpc`: REST and ConnectRPC clients
- `@hop-top/kit/safety`: `--force` delegation guard the policy builds on
- Go reference: [`go/transport/mcpsdk`](../../../../go/transport/mcpsdk/README.md); Python port: [`hop_top_kit/mcp`](../../../py/hop_top_kit/mcp/README.md)

## See also

- [`sdk/ts/README.md`](../../README.md), section "MCP surface": options, safety, confirmation, scope
- [`docs/adopters/guides/serve-mcp-from-any-sdk.md`](../../../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
- [`docs/adopters/guides/expose-cli-over-mcp.md`](../../../../docs/adopters/guides/expose-cli-over-mcp.md): the Go reference
