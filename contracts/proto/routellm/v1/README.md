# routellm/v1

## What it answers

The `routellm.v1` gRPC surface an LLM router exposes: `RouterService` (get/update config, list routers), `EvaService` (contracts and evaluations) and `HealthService` (check, watch stream). Wrong place for the kit-side client behaviour; `go/ai/llm/routellm` holds that and does not import these stubs.

## Use it when

- you build or mock a router that kit talks to and need the message shapes
- you change one of the three `.proto` files: regenerate `gen/go` in the same commit

## Quick start

```sh
make proto
```

Runs `buf generate` here (and in `../../crud/v1`); `buf.gen.yaml` writes Go protobuf and gRPC stubs to `gen/go/` with `paths=source_relative`. Generated files are committed.

## Contract

| File | Service | RPCs |
|------|---------|------|
| `router.proto` | `RouterService` | `GetConfig`, `UpdateConfig`, `ListRouters` |
| `eva.proto` | `EvaService` | `ListContracts`, `AddContract`, `RemoveContract`, `Evaluate`, `GetEvalResults` |
| `health.proto` | `HealthService` | `Check`, `Watch` (server stream) |

Go package: `hop.top/kit/contracts/proto/routellm/v1;routellmv1`. No package under `go/` or `sdk/` imports the generated stubs today; `gen/go/` is the only artefact.

## Neighbours

- `../../crud/v1/`: generic entity service, Connect stubs for Go and TypeScript
- `go/ai/llm/routellm/`: kit's router client and config reload

## See also

- [`go/ai/llm/README.md`](../../../../go/ai/llm/README.md)
