# mcpserve

## What it answers

How a kit CLI serves its command tree as MCP tools under `<tool> serve`,
with the same lifecycle and gates as the `api` and `socket` services,
without every other kit CLI linking the MCP SDK. `mcpserve.With`
registers the `mcp` service; the protocol is the official Go SDK via
[`go/transport/mcpsdk`](../../../transport/mcpsdk/README.md).

## Use it when

- serve MCP over streamable HTTP → `mcpserve.With(mcpserve.Config{})`, then `<tool> serve mcp`
- serve a desktop host that spawns the tool → `<tool> serve mcp --stdio`
- expose it beyond loopback → `Config.Auth` plus `--policy`, or the `services.mcp.insecure_*` opt-ins
- permit destructive commands over MCP → `Config.Policy.AllowDestructiveOn` naming `cmdsurface.SurfaceMCP`
- add prompts or resources → `Config.ServerOptions` with `mcpsdk.WithServerConfigurator`
- tune the HTTP listener's middleware (body limit, Host allowlist, compression, health, metrics scrape) → `services.mcp.<block>`, as on the `api` service

## Quick start

```go
root := cli.New(cli.Config{Name: "mytool", Version: version},
    cli.WithAPI(cli.APIConfig{}),
    mcpserve.With(mcpserve.Config{}),
)
```

## Contract

- Normative text: [serve-lifecycle contract §"The mcp service"](../../../../docs/contracts/serve-lifecycle.md#the-mcp-service).
- Disabled by default; `serve mcp` starts it. HTTP on its own listener, default `127.0.0.1:8081`, path `/mcp`.
- Keys: `services.mcp.{transport,addr,path,insecure_remote,insecure_no_policy}`; flags `--stdio`, `--mcp-addr`.
- HTTP over TLS under `services.mcp.tls`; `services.mcp.auth.mode: mtls` authenticates by client certificate in place of `Auth`.
- HTTP-plane middleware on the HTTP transport: the chain every kit listener shares, from `services.mcp.<block>`; refusals are JSON-RPC errors. The SDK's body cap follows `body_limit`; its DNS-rebinding check stays beneath kit's Host check unless `host_check` admits other names.
- `kit/auth-required`: HTTP needs `Config.Auth`; stdio admits on the spawn's trust.
- `kit/requires-confirmation`: an accepted elicitation, or `X-Confirm-Token` over HTTP; asked only after every machine gate.
- HTTP server timeouts from `services.mcp.timeouts.*` (then `services.all.timeouts.*`); the endpoint is a stream route, exempt from `write`.
- `timeouts.command` or a command's `kit/timeout` bounds each call; past it the result is `isError`, text `deadline_exceeded: …`, `_meta["hop.top/refusal"].code`.
- stdio: stdout carries only protocol messages; end of input exits 0 once every request already read is answered.
- `go/console/cli` does not import this package or the SDK; a test in `go/console/cli` pins that.
