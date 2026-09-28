# cmdsurface/v1

## What it answers

The `cmdsurface.v1.Commands` wire contract: `Invoke` (unary) and `InvokeStream` (server-streaming) over the leaves of a CLI command tree that a cmdsurface Bridge exposes on the `rpc` surface. `commands.proto` is the schema source of truth for that surface; the Go types in `go/transport/cmdsurface` mirror it. Wrong place for a domain service; define its own proto package.

## Use it when

- you call a kit CLI's RPC surface from another language: generate a client from `commands.proto` with any protobuf or Connect toolchain
- you change `commands.proto`: regenerate and commit the stubs in the same commit

## Quick start

```sh
make proto        # regenerate every proto module
make proto-check  # lint, regenerate, fail when committed stubs differ
```

Generated files are committed so `go get` works without `buf`. CI runs `make proto-check`.

## Contract

| Output | Plugin (pinned) | Where |
|--------|-----------------|-------|
| `commands.pb.go` | `buf.build/protocolbuffers/go` | `go/transport/cmdsurface/gen/cmdsurfacev1/` |
| `cmdsurfacev1connect/commands.connect.go` | `buf.build/connectrpc/go` | same |

JSON keys are snake_case (`exit_code`, `request_id`): each multi-word field sets `json_name`, so the proto3 JSON mapping matches the Go struct tags. `Result.exit_code` has presence, so a zero exit code stays on the wire. `Result.data` carries a number a double cannot hold exactly as a string of its digits; `Result.data_json` carries the whole payload with every digit intact.

Plugin versions in `buf.gen.yaml` follow `google.golang.org/protobuf` and `connectrpc.com/connect` in `go.mod`; bump them together.

## Neighbours

- `../../crud/v1/`: generic CRUD entity service
- `../../../bridge.proto`: kit/bridge payload, not a service

## See also

- [`go/transport/cmdsurface/README.md`](../../../../go/transport/cmdsurface/README.md)
