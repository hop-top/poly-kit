# Serving and transports

Open when: exposing a command tree to other processes or agents, adding
authentication or limits to served commands, or writing a transport.

## One tree, many surfaces

A tree built with `cli.New` is served by options, nothing mounted by hand:
`<tool> service start [api|socket|mcp|rpc]`. The `api` service is on by
default; the others are off until named.

| Need | Option or package |
|------|-------------------|
| REST under `/v1/commands`, OpenAPI, discovery | `service.WithAPI(service.APIConfig{})` in `go/console/cli/service` |
| owner-only Unix socket, NDJSON | `service.WithSocket(service.SocketConfig{})` |
| MCP tools over HTTP or `--stdio` | `mcpserve.With(mcpserve.Config{})` in `go/console/cli/mcpserve` (links the MCP SDK; `cli` does not) |
| Connect, gRPC, gRPC-Web | `rpcserve.With(rpcserve.Config{})` in `go/console/cli/rpcserve` |
| a service of your own under the same supervisor | `service.WithService(svc)` |
| who may run what, per surface | `service.WithPermission`, `service.WithPermissionRules`, `--policy` files |
| audit served calls | `service.WithAuditSinks`, `service.WithAuditCommand` |
| tokens and API keys for remote callers | `cli.WithIdentity`, `service.WithAPIKeys`; verification in `go/transport/authn` |
| parallel invocations | `service.WithRootFactory` (a tree per call) |
| a tree already built, no `service` group | `go/transport/cmdsurface`: `New(root)`, `Expose`, then a `Mount*` per surface |
| a new transport kind | `go/transport/transportsvc` |
| OpenTelemetry for served calls | `go/transport/observability` |
| payload types routed to handlers | `go/bridge` |

## Gates a served leaf passes

Annotations decide how a command is served; set them on the leaf.
`kit/side-effect` picks GET versus POST and the tier; destructive leaves are
withheld from every remote surface until `Policy.AllowDestructiveOn` names
it; `kit/auth-required` needs an established caller (a verifier accepted a
credential, or the transport proves it: socket, stdio); `kit/requires-confirmation`
asks a person (MCP elicitation or a confirm token over HTTP); `kit/args`
names positional operands so schemas can carry them; interactive and
self-hosting leaves are never served. `kit/withhold-from` keeps one leaf off
chosen surfaces.

## Traps

- HTTP binds loopback. Remote binding needs authentication and a policy, or
  the explicit `insecure_*` keys; do not set those to make a demo work.
- MCP over stdio: stdout is the protocol. Any print goes to stderr while it
  serves; the snippet test shows the wire.
- The `token` group is kit-reserved: never a served tool.
- `MountREST` and `MountMCP` are deprecated; use the services above.
- Output over a served surface is captured and capped (16 MiB default);
  declare `cli.SetOutputSchema` so `data` is real, not scraped text.

## Pages

- [manage the service lifecycle](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/manage-the-service-lifecycle.md),
  [serve-lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md),
  [migrate to served commands](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/migrate-to-served-commands.md)
- [expose over MCP](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/expose-cli-over-mcp.md),
  [REST](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/expose-cli-over-rest.md),
  [gRPC](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/expose-cli-over-grpc.md),
  [Unix socket](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-cli-over-unix-socket.md)
- [secure remote serving](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/secure-remote-serving.md),
  [served middleware](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/served-middleware.md),
  [served observability](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/served-observability.md)
- [cmdsurface](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/cmdsurface.md),
  [build a transport service](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/build-a-transport-service.md),
  [route bridge payloads](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/route-bridge-payloads.md)
