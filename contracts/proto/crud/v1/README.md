# crud/v1

## What it answers

The `crud.v1.EntityService` wire contract: generic Create/Get/List/Update/Delete over `google.protobuf.Struct` payloads, so a caller works with any entity schema without per-type proto definitions. Wrong place for a typed, domain-specific service; define its own proto package.

## Use it when

- you expose a resource over `go/transport/rpc`: the Go stubs here are what `rpc.Resource` and `rpc/client` speak
- you change `crud.proto`: regenerate and commit the stubs, Go and TypeScript, in the same commit

## Quick start

```sh
make proto
```

Runs `buf generate` in this directory (and in `../../routellm/v1`). Generated files are committed so `go get` works without `buf`.

## Contract

`crud.proto` defines `EntityService` and its request/response messages; `ListRequest` carries `limit`, `offset` and `sort`. `buf.gen.yaml` emits, with managed mode on:

| Output | Plugin | Where |
|--------|--------|-------|
| `crud.pb.go` | `buf.build/protocolbuffers/go` | here, `paths=source_relative` |
| `crudv1connect/crud.connect.go` | `buf.build/connectrpc/go` | here |
| `crud_pb.ts`, `crud_connect.ts` | `buf.build/bufbuild/es`, `buf.build/connectrpc/es` | `sdk/ts/src/gen/` |

Consumers: `go/transport/rpc/resource.go` and `go/transport/rpc/client/client.go` import `crudv1`; the rpc tests import `crudv1connect`; `sdk/ts/src/rpc.ts` imports `./gen/crud_pb.js`.

## Neighbours

- `../../routellm/v1/`: LLM router protos, gRPC stubs only
- `../../../bridge.proto`: kit/bridge payload, not a service

## See also

- [`go/transport/rpc/README.md`](../../../../go/transport/rpc/README.md)
