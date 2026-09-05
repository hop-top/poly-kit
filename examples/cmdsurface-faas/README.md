# cmdsurface FaaS example

## What it answers

How to ship a `cmdsurface.Bridge` into a managed runtime with the
package's FaaS adapters: two separate binaries, one per target, built
from one shared tree. For exploring every surface locally in one process
use [`examples/cmdsurface`](../cmdsurface/README.md) instead.

| Target     | Adapter         | Entry point            |
| ---------- | --------------- | ---------------------- |
| AWS Lambda | `LambdaHandler` | `cmd/lambda/main.go`   |
| Cloud Run  | `RunCloudRun`   | `cmd/cloudrun/main.go` |

## Use it when

- you deploy one leaf per Lambda function → `cmd/lambda`, vary
  `CMDSURF_LEAF` and `CMDSURF_EVENT` per function
- you deploy a request-scoped HTTP container → `cmd/cloudrun` (REST +
  SSE + MCP behind `$PORT`, SIGTERM drain)
- you need telemetry in a managed runtime → `shared.MaybeBuildTelemetry`,
  gated on `CMDSURFACE_DEMO_TELEMETRY=1`

## Quick start

Cloud Run, locally:

```sh
go run ./examples/cmdsurface-faas/cmd/cloudrun
# in another shell:
curl -X POST http://localhost:8080/cmd/ping
# → {"exit_code":0,"stdout":"pong\n"}

curl -N http://localhost:8080/cmd/ping/stream
# → event: event
#   data: {"kind":"stdout","data":"pong","at":"..."}
#   event: result
#   data: {"exit_code":0,"stdout":"pong\n"}

curl -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Lambda, with the Runtime Interface Emulator:

```sh
# Build the bootstrap binary the Lambda runtime expects.
GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
    -o bootstrap ./examples/cmdsurface-faas/cmd/lambda

# Run under the official emulator (aws-lambda-rie).
aws-lambda-rie ./bootstrap

# In another shell, simulate an API Gateway V2 invoke:
curl -X POST http://localhost:8080/2015-03-31/functions/function/invocations \
  -d '{"body":"{\"message\":\"hi\"}","headers":{"content-type":"application/json"}}'
```

## Contract

- `shared/bridge.go` builds an identical tree (`echo`, `ping`, `stamp`)
  under an identical policy; only the adapter differs between deploys.
- The bridge is built once at module scope; warm invocations reuse it.
- `CMDSURF_EVENT` is one of `apigw_v2`, `apigw_v1`, `eventbridge`, `sqs`,
  `direct`. All but `direct` validate the leaf path at `LambdaHandler`
  construction, so a bad `CMDSURF_LEAF` fails the cold start.
- Cloud Run honours SIGTERM with a 9-second drain
  (`CloudRunConfig.ShutdownGrace`).

## Neighbours

- `go/transport/cmdsurface`: `LambdaHandler`, `RunCloudRun` and the bridge
- `examples/cmdsurface`: every surface in one process
- `cmd/cloudrun/Dockerfile`, `cmd/lambda/Dockerfile.example`: container
  builds

## See also

- [cmdsurface example walkthrough](../../docs/adopters/guides/cmdsurface-example.md#faas-binaries-examplescmdsurface-faas):
  deployment recipes, event-type matrix, cold start notes, telemetry,
  differences from the unified example
