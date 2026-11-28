# Serving and transports

Open when a tool needs a long-running `serve` command, a service lifecycle,
or its commands callable as MCP tools. References:
[Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md), the
[serve contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md) and the guides
[serve MCP with the SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-with-the-sdk.md) and
[manage the service lifecycle](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/manage-the-service-lifecycle.md).

## Decisions

- Services: `hop_top_kit.serve`. Implement the `Service` protocol, add it to
  a `ServiceRegistry`, then `register_serve(app, ServeOptions(registry=...,
  configs={...}))`. It mounts `serve` and `serve <service>` only, on asyncio,
  with the same readiness, shutdown and exit codes as every port.
- MCP: needs the `mcp` extra. Build `Command` and `Bridge` from
  `hop_top_kit.mcp`, then `mount_mcp(bridge)`. The result is an ASGI callable
  (uvicorn, hypercorn, a Starlette route); `surface.handle(Request)` is the
  same surface as a pure function. A host that spawns the tool:
  `serve_stdio(bridge)`. See [mcp.py](../assets/snippets/mcp/mcp.py).
- Exposure is default-closed: destructive leaves are blocked unless policy
  allows them, and `audit=fn` receives each refusal. Confirmation on the
  modern era: `header_confirmation_gate` or `ElicitationConfirmationGate`.
- The bridge covers the MCP leg of command exposure only. REST, ConnectRPC,
  unix socket, authenticated serving and observability have no module here:
  [other ports](other-ports.md); a REST or gRPC front end means Go or the
  sidecar.

## Check coverage first

Cell `unverified`: served middleware (MCP audit and safety only, in
`hop_top_kit.mcp`). Cell `partial`: the command surface bridge.
