# rpc

## What it answers

How a kit-rs tool serves Connect, gRPC and gRPC-Web on one port (HTTP/1.1 and h2c), with the interceptors Go's `go/transport/rpc` ships. Port of that package. The tool's command tree as an RPC service, and REST, are elsewhere (`api_server` for REST).

## Use it when

- serve procedures → `RpcServer::builder(addr).service(...).build()`, registered on a `serve::ServiceRegistry` as service `rpc`
- serve `crud.v1.EntityService` → implement `EntityService` (owned protobuf messages, `RequestContext`, `RpcError`), mount `EntityRpc::new(it)`
- serve a tool's own proto → generate stubs with `protoc-gen-connect-rust`, register through `Mount::generated` from an `RpcService`
- give calls a request id, log them, verify callers → `.interceptor(request_id())`, `.interceptor(log(|l| ...))`, `.interceptor(authenticate(auth_func).challenge(..).on_refused(..))`
- write your own interceptor → implement `Interceptor` (`intercept(call, next)`), unary and streaming alike
- read who called in a handler → `ctx.authenticated()`, `ctx.claims()`, `ctx.request_id()`
- bound request size, observe handler panics → `.max_body_bytes(n)`, `.on_panic(|msg| ...)`

## Quick start

```rust
struct Widgets;
impl EntityService for Widgets {
    async fn get(&self, _ctx: &RequestContext, req: GetRequest) -> Result<GetResponse, RpcError> {
        Err(RpcError::not_found(format!("widget {} not found", req.id)))
    }
    // create, list, update, delete likewise
}

let server = RpcServer::builder("127.0.0.1:8082")
    .service(EntityRpc::new(Widgets))
    .interceptor(request_id())
    .interceptor(authenticate(verifier.auth_func()))
    .build();
registry.register(Arc::new(server))?; // then Supervisor::run, as in `serve`
```

```sh
buf curl --schema contracts/proto/crud/v1 --data '{"id":"w-1"}' \
  http://127.0.0.1:8082/crud.v1.EntityService/Get
```

Verified by `tests/rpc_server.rs` and the shared corpus `tests/rpc_conformance.rs`.

## Contract

- Feature `rpc-server` (native): `connectrpc =0.9.1` (default features off; `axum`, `json`) with `buffa =0.9.2` / `buffa-types =0.9.2`, on the `api-server` axum stack. No new transitive crate is younger than 14 days. Vetting record: the comment above the pins in `Cargo.toml`.
- One listener answers Connect (JSON and proto), gRPC and gRPC-Web. Native gRPC is HTTP/2 with prior knowledge, no TLS (h2c); Connect and gRPC-Web work over either HTTP version. Parity with Go on the wire: [`contracts/rpc-v1`](../../../../../contracts/rpc-v1/README.md).
- Generated code is committed (`gen/`), from `contracts/proto/crud/v1` through the plugins pinned in `buf.gen.rs.yaml` (`protoc-gen-buffa` 0.9.2, `protoc-gen-buffa-packaging` 0.9.2, `protoc-gen-connect-rust` from `connectrpc-codegen` 0.9.0; checksums in that file). `make proto-rs` regenerates, `make proto-rs-check` fails on drift. No `build.rs`, no `protoc` at build time. Bump the runtime pins and the plugins together.
- Boundary: kit's own `RpcService`, `Interceptor`, `Call`, `RequestContext`, `RpcError`, `Code`. connectrpc, buffa, axum and tower stay inside. Two things cross by design: protobuf message types (`rpc::proto`, the data contract) and generated stubs, which are connectrpc-typed and meet the server only at `Mount::generated` (`rpc::connect` exposes the generated clients and traits).
- Interceptors run after the body is read (within the body limit), in registration order, first outermost, for unary calls and for stream establishment. `authenticate` verifies with kit-rs authn (`Authorization` and `X-API-Key`), refuses `unauthenticated` in the caller's protocol before any handler runs, and attaches `Claims` and `Authenticated` for the handler. A credential check that must refuse before the body is read belongs in a tower layer in front, as in the REST server.
- A handler panic is `internal` with the detail withheld (Go's recovery interceptor includes it), HTTP 500 for Connect and a trailers-only `grpc-status: 13` reply for gRPC and gRPC-Web; `on_panic` sees the message. Recovery is tower-http's catch-panic layer plus a shaping layer that gives the reply its protocol.
- The body limit is enforced twice: connectrpc's `Limits` (also bounds one message) and tower-http's body limit. Default 4 MiB.
- Not ported: Go's `RPCResource` over `api.Service[T]` (needs a Rust `api.Service`), the typed client (`rpc/client`), the command-tree service (`cmdsurface.MountRPC`), per-call write-timeout lifting.

## Neighbours

- `hop_top_kit::serve`: the lifecycle this is a `Service` of
- `hop_top_kit::authn`: the `AuthFunc` `authenticate` calls
- `hop_top_kit::api_server`: the REST server on the same stack
- `go/transport/rpc`: the reference

## See also

- [`contracts/rpc-v1`](../../../../../contracts/rpc-v1/README.md), [`contracts/proto/crud/v1`](../../../../../contracts/proto/crud/v1/README.md)
- [serve lifecycle contract](../../../../../docs/contracts/serve-lifecycle.md)
