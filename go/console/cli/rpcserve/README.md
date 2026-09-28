# rpcserve

## What it answers

How a kit CLI serves its command tree as the published
`cmdsurface.v1.Commands` service under `<tool> serve`, with the same
lifecycle and gates as the `api`, `socket` and `mcp` services, without
every other kit CLI linking the RPC server. `rpcserve.With` registers
the `rpc` service; the handler is `cmdsurface.MountRPC` over the
generated Connect handler for
[`commands.proto`](../../../../contracts/proto/cmdsurface/v1/commands.proto).

## Use it when

- serve Connect, gRPC and gRPC-Web clients → `rpcserve.With(rpcserve.Config{})`, then `<tool> serve rpc`
- expose it beyond loopback → `Config.Auth` plus `--policy`, or the `services.rpc.insecure_*` opt-ins
- permit destructive commands over RPC → `Config.Policy.AllowDestructiveOn` naming `cmdsurface.SurfaceRPC`
- bound request size → `Config.MaxBodyBytes` (default 4 MiB)

## Quick start

```go
root := cli.New(cli.Config{Name: "mytool", Version: version},
    cli.WithAPI(cli.APIConfig{}),
    rpcserve.With(rpcserve.Config{}),
)
```

```sh
mytool serve rpc
buf curl --schema contracts/proto/cmdsurface/v1 \
  --data '{"path":["item","list"]}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/Invoke
```

## Contract

- Normative text: [serve-lifecycle contract §"The rpc service"](../../../../docs/contracts/serve-lifecycle.md#the-rpc-service).
- Disabled by default; `serve rpc` starts it. Own listener, default `127.0.0.1:8082`; HTTP/1.1 and h2c on one port. Readiness carries the base URL.
- Keys: `services.rpc.{addr,insecure_remote,insecure_no_policy}`; flag `--rpc-addr`.
- `Auth` (`api.AuthFunc`) gates every procedure, unary and streaming. Identity comes from what it verified; the body's `meta.caller`, `meta.tenant` and `meta.extra` are dropped.
- `kit/auth-required` needs verified `Auth`; `kit/requires-confirmation` needs `X-Confirm-Token`.
- `InvokeStream` is exempt from the server's write timeout; stopping the service ends open streams.
- `go/console/cli` does not import this package or `go/transport/rpc`; a test in `go/console/cli` pins that.
