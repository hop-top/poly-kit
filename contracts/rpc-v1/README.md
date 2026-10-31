# rpc-v1

## What it answers

What the rpc server must put on the wire for the shared `crud.v1.EntityService`, independent of language: the Connect JSON exchanges (HTTP status, content type, body) for create, get, list, update, delete, the not-found refusal, a malformed body, a handler panic and the authenticate interceptor's refusal. Wrong place for protocol coverage (gRPC and gRPC-Web are exercised by each runner against its own client) and for the interceptors' option surface.

## Use it when

- you change what `go/transport/rpc` answers on the wire: add a step first, then make every port pass it
- you port the rpc server to another SDK: run `vectors.json` against it

## Quick start

```sh
make test-parity-rpc
# or separately:
go test ./go/transport/rpc/ -run '^TestConformanceRPC'
cargo test --features rpc-server --test rpc_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. The file carries `version: "v1"` and a `description` stating the fixture handler each group runs against. Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run.

| Field | Pins |
|-------|------|
| `groups[].steps[]` `{procedure, request \| raw, headers, status, response \| code_only}` | one Connect JSON unary exchange, in order, against that group's server |
| `response` | the exact JSON body (compared as JSON values) |
| `code_only` | the Connect error `code` alone, where the message is the implementation's own |
| `groups[].auth` | the group sits behind `authenticate`; only `Authorization: Bearer <token>` is admitted |

Known divergence, not vectored: a handler panic. Go's recovery interceptor answers `internal` with `panic: <detail>` in the message; the Rust server withholds the detail. Both answer HTTP 500 and `internal`, which is what the vector pins.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/transport/rpc/conformance_test.go` (`TestConformanceRPC*`) |
| Rust | `sdk/experimental/rs/tests/rpc_conformance.rs` |

## See also

- [`go/transport/rpc`](../../go/transport/rpc/README.md), the Go reference
- [`contracts/proto/crud/v1`](../proto/crud/v1/README.md), the proto both servers speak
