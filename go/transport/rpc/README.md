# rpc

## What it answers

How a kit tool serves ConnectRPC handlers: the server scaffold that
answers Connect, gRPC and gRPC-Web on one port (HTTP/1.1 and h2c), the
interceptors that mirror `go/transport/api`'s middleware, the generic
CRUD entity service, and its typed client. The tool's own command tree
as an RPC service is `cmdsurface.MountRPC`, and `rpcserve.With` serves
it under `<tool> serve`; REST is `go/transport/api`.

## Use it when

- serve the command tree over gRPC with no wiring → `rpcserve.With(rpcserve.Config{})` in `go/console/cli/rpcserve`
- mount any Connect handler → `rpc.NewServer()`, `Server.Handle(path, handler)`, `rpc.ListenAndServe(ctx, addr, srv)`
- bind the listener yourself (readiness, `:0` ports) → `Server.HTTPServer()`, then `Serve(ln)` and `Shutdown`
- authenticate unary and streaming calls with the REST `api.AuthFunc` → `rpc.Authenticate(fn, rpc.OnAuthRefused(...))`
- read who called → `rpc.Authenticated(ctx)`, `rpc.ClaimsFromContext(ctx)`, then `api.IdentityOf`
- request ids, logging, panic recovery on unary calls → `RequestIDInterceptor`, `LogInterceptor`, `RecoveryInterceptor`
- CRUD over any `api.Service[T]` without codegen → `rpc.RPCResource[T]`, client `rpc/client.New[T]`

## Quick start

```go
import (
    "connectrpc.com/connect"

    "hop.top/kit/go/transport/rpc"
)

srv := rpc.NewServer(
    rpc.WithInterceptors(rpc.Authenticate(authFn)),
)
path, handler := rpc.RPCResource[Widget](store,
    connect.WithInterceptors(srv.Interceptors()...),
)
srv.Handle(path, handler)
err := rpc.ListenAndServe(ctx, "127.0.0.1:8082", srv)
```

```sh
buf curl --schema contracts/proto/crud/v1 \
  --data '{"id":"w-1"}' \
  http://127.0.0.1:8082/crud.v1.EntityService/Get
```

## Contract

- One port, three protocols: Connect (proto and JSON) and gRPC-Web over HTTP/1.1 or HTTP/2; native gRPC over HTTP/2, served without TLS as h2c (`http.Server.Protocols`). A gRPC client dials `host:port` in plaintext mode.
- Defaults: read timeout 5s, write timeout 10s, shutdown 30s (`WithReadTimeout`, `WithWriteTimeout`, `WithShutdownTimeout`). The write timeout cuts a server stream that runs longer; lift it per call with `http.ResponseController.SetWriteDeadline`, as the `rpc` service does for `InvokeStream`.
- `Authenticate` runs on every handler call, unary and streaming, before the handler. A refusal is `unauthenticated` in the caller's protocol. The `AuthFunc` sees a synthetic request: the call's headers, context and peer address only.
- `AuthInterceptor` is deprecated: it wraps unary calls only, so a streaming procedure behind it runs unauthenticated.
- `RPCResource` maps `domain.ErrNotFound`, `ErrConflict`, `ErrValidation` and `ErrInvalidTransition` to `not_found`, `already_exists`, `invalid_argument` and `failed_precondition`; anything else is `internal` with the detail withheld.
- `rpc/client` returns every Connect error as an `*api.APIError` whose code is the Connect code (`not_found`, …) and whose status is the code's HTTP mapping. A `resource_exhausted` carrying `Retry-After` — a rate-limit refusal — is a `*client.RateLimitedError`: status 429, code `rate_limited`, `errors.Is(err, api.ErrRateLimited)`, and the wait in `RetryAfter`.
- Importing this package registers the `crud.v1` protobuf types at init. `go/console/cli` does not import it, so a CLI that serves no RPC does not carry them.

## Neighbours

- `go/transport/cmdsurface`: `MountRPC`, the command tree as `cmdsurface.v1.Commands`.
- `go/console/cli/rpcserve`: the built-in `rpc` service over `MountRPC` and this server.
- `go/transport/api`: REST, and the `AuthFunc` and claims types shared here.
- `go/transport/rpc/client`: typed client for `RPCResource`.

## See also

- [expose-cli-over-grpc.md](../../../docs/adopters/guides/expose-cli-over-grpc.md): the task walkthrough
- [cmdsurface reference §RPC](../../../docs/adopters/reference/cmdsurface.md#rpc)
- [`contracts/proto/cmdsurface/v1`](../../../contracts/proto/cmdsurface/v1/README.md), [`contracts/proto/crud/v1`](../../../contracts/proto/crud/v1/)
- [serve lifecycle contract §"The rpc service"](../../../docs/contracts/serve-lifecycle.md#the-rpc-service)
