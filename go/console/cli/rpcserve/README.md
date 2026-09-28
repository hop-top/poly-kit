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
- bound request size → `services.rpc.body_limit.max_bytes`, or `Config.MaxBodyBytes` in code (default 4 MiB)
- tune the listener's HTTP-plane middleware (Host allowlist, compression, health, metrics scrape) → `services.rpc.<block>`, as on the `api` service
- meter, trace or rate-limit admitted calls → `Config.Interceptors`, run inside kit's gates

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

Task guide: [expose your CLI over gRPC](../../../../docs/adopters/guides/expose-cli-over-grpc.md).

## Contract

- Normative text: [serve-lifecycle contract §"The rpc service"](../../../../docs/contracts/serve-lifecycle.md#the-rpc-service).
- Disabled by default; `serve rpc` starts it. Own listener, default `127.0.0.1:8082`; HTTP/1.1 and h2c on one port, or TLS with HTTP/2 by ALPN under `services.rpc.tls`. Readiness carries the base URL.
- `services.rpc.auth.mode: mtls` authenticates by client certificate in place of `Auth`.
- Keys: `services.rpc.{addr,insecure_remote,insecure_no_policy}`; flag `--rpc-addr`.
- HTTP-plane middleware: the chain every kit listener shares (request id through compression, health probes, Host and Origin checks, metrics endpoint), from `services.rpc.<block>`. Refusals are Connect errors in the caller's protocol. Connect's read limit and per-message compression follow `body_limit` and `compression`.
- `Auth` (`api.AuthFunc`) gates every procedure, unary and streaming. Identity comes from what it verified; the body's `meta.caller`, `meta.tenant` and `meta.extra` are dropped.
- `kit/auth-required` needs verified `Auth`; `kit/requires-confirmation` needs `X-Confirm-Token`.
- `Config.Interceptors` see only calls every gate admitted; a call they refuse does not run and is audited.
- No CORS: a browser on another origin needs a proxy in front.
- Server timeouts from `services.rpc.timeouts.{read_header,read,write,idle}` (then `services.all.timeouts.*`); `InvokeStream` is exempt from the write timeout; stopping the service ends open streams.
- `timeouts.command` or a command's `kit/timeout` bounds each call; past it the call fails `CodeDeadlineExceeded`.
- `go/console/cli` does not import this package or `go/transport/rpc`; a test in `go/console/cli` pins that.
