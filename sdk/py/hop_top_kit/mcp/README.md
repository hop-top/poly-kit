# mcp

## What it answers

How a command tree is served over the Model Context Protocol from one mount that answers both the `2024-11-05` handshake era and the `2026-07-28` stateless era, choosing per request. Wrong module for the service lifecycle that hosts the mount (`hop_top_kit.serve`) and for the `--force` guard itself (`hop_top_kit.safety`).

## Use it when

- expose leaves as MCP tools: build `Command` / `Bridge`, then `mount_mcp(bridge, **options)`
- host it: `McpSurface` is an ASGI callable; mount it under uvicorn, hypercorn or a Starlette route
- bind to something else: `McpSurface.handle(Request) -> Response` is a pure function, no socket
- require confirmation on the modern era: `header_confirmation_gate` or `ElicitationConfirmationGate`

## Quick start

```python
from hop_top_kit.mcp import Bridge, Command, Request, Result, mount_mcp

root = Command(name="app", children=[
    Command(
        name="ping",
        short="Ping the server",
        run=lambda flags: Result(stdout="pong\n"),
        annotations={"kit/side-effect": "read"},
    ),
])
surface = mount_mcp(Bridge(root))   # an ASGI callable
res = surface.handle(Request(body=b'{"jsonrpc":"2.0","id":1,"method":"tools/list"}'))
print(res.status, res.body)
```

## Contract

- Install the extra: `pip install 'hop-top-kit[mcp]'`; the base package imports none of the protocol libraries.
- Protocol layer is `mcp>=2.0,<3` plus `mcp-types>=2.0,<3`; the 1.x line tops out at `2025-11-25` and cannot serve the modern era.
- Wire bytes are pinned by [`sdk/tests/cross-lang/fixtures/mcp-wire.json`](../../../tests/cross-lang/fixtures/mcp-wire.json), compared as raw bytes; `tests/test_mcp_conformance.py` replays both `cases` and `sequences`.
- `legacy` and `modern` are separate modules with `dispatch` choosing between them; the legacy path is preserved byte for byte, additive only.
- `tools/list` re-reads the bridge's leaves per request; caching the leaf set fails the sequence fixture.
- ASGI, not WSGI: the modern era's streaming is not expressible in WSGI.
- Parity: [`contracts/parity/README.md`](../../../../contracts/parity/README.md), section "MCP wire contract".

## Neighbours

- `hop_top_kit.serve`: service lifecycle hosting the mount
- `hop_top_kit.safety`: `--force` delegation guard the policy builds on
- `hop_top_kit.scope`: filesystem path policy
- Go reference: [`go/transport/mcpsdk`](../../../../go/transport/mcpsdk/README.md); TypeScript port: [`sdk/ts/src/mcp`](../../../ts/src/mcp/README.md)

## See also

- [`sdk/py/README.md`](../../README.md), section "MCP surface": options, safety, confirmation, lazy help flags, scope
- [`docs/adopters/guides/serve-mcp-from-any-sdk.md`](../../../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
- [`docs/adopters/guides/expose-cli-over-mcp.md`](../../../../docs/adopters/guides/expose-cli-over-mcp.md): the Go reference
