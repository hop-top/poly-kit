# routellm

LLM router service definitions.

## Services

- `RouterService` (`router.proto`): router config get/update, router listing.
- `EvaService` (`eva.proto`): contract CRUD, evaluation, eval results.
- `HealthService` (`health.proto`): unary `Check`, streaming `Watch`.

## Why the generated Go stubs exist

`gen/go/` has no in-repo Go importer: `go/ai/llm/routellm` is a native
in-process router and does not dial gRPC. The stubs are kept deliberately.

- These protos are the control-plane wire contract, not an internal helper.
  `sdk/py/hop_top_kit/routellm_grpc.py` serves all three services and
  declares its hand-written dataclass messages a placeholder "until
  buf-generated protobuf code is available".
- Generated Go is the reference encoding other languages are checked
  against. Deleting it would leave the contract with no generated artifact
  in the repo and no way to detect drift between `.proto` and the Python
  server.
- `.golangci.yml` already exempts `go/ai/llm/routellm/` from `unused`,
  so absence of an importer is expected, not an oversight.

Regenerate with `make proto`.
