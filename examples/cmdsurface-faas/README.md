# cmdsurface FaaS example

Two deployable demos that front the same `cmdsurface.Bridge` with the
package's FaaS adapters:

| Target    | Adapter               | Entry point          |
| --------- | --------------------- | -------------------- |
| AWS Lambda | `LambdaHandler`       | `cmd/lambda/main.go` |
| Cloud Run  | `RunCloudRun`         | `cmd/cloudrun/main.go` |

Both binaries import `shared/bridge.go`, which builds an identical
tree (`echo`, `ping`, `stamp`) under an identical policy. The only
thing that differs between deploys is the adapter that fronts the
bridge.

## What this is

`examples/cmdsurface/` (Waves 1–3) is the **unified-binary** example:
a single process that mounts every surface (REST, RPC, MCP, WS, SSE,
Bus, Cron, Webhook, OAuth, Signed) locally for development. This
example is the opposite shape: **two separate binaries** built to fit
two managed-runtime contracts.

- Lambda is **single-event-handler**: one function per leaf, mapped
  via `LambdaConfig.Mapping`. Bridge constructed once at module init;
  every warm invocation reuses it.
- Cloud Run is **containerised, request-scoped HTTP**: one binary
  serving REST + SSE + MCP behind `$PORT`, with SIGTERM-driven drain.

## Run locally

### Cloud Run

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

### Lambda (with the Runtime Interface Emulator)

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

## Cloud Run deployment

The Dockerfile lives at `cmd/cloudrun/Dockerfile`. Deploy from the
repo root:

```sh
gcloud run deploy cmdsurface-faas-demo \
    --source . \
    --region us-central1 \
    --allow-unauthenticated
```

`gcloud` picks up `cmd/cloudrun/Dockerfile` automatically via
`--source`. If you build the image yourself:

```sh
docker build -t cmdsurface-faas-cloudrun \
    -f examples/cmdsurface-faas/cmd/cloudrun/Dockerfile .
gcloud run deploy cmdsurface-faas-demo \
    --image gcr.io/<project>/cmdsurface-faas-cloudrun \
    --region us-central1
```

The container listens on `$PORT` (default 8080) and honours
SIGTERM with a 9-second drain (`CloudRunConfig.ShutdownGrace`).

## Lambda deployment (zip bundle)

This is the canonical Lambda deploy. Build a static `bootstrap`
binary, zip, and push:

```sh
GOOS=linux GOARCH=arm64 go build \
    -tags lambda.norpc \
    -trimpath -ldflags='-s -w' \
    -o bootstrap ./examples/cmdsurface-faas/cmd/lambda
zip function.zip bootstrap

aws lambda create-function \
    --function-name cmdsurface-ping \
    --runtime provided.al2023 \
    --architectures arm64 \
    --handler bootstrap \
    --zip-file fileb://function.zip \
    --role arn:aws:iam::<acct>:role/lambda-basic-execution \
    --environment 'Variables={CMDSURF_EVENT=apigw_v2,CMDSURF_LEAF=ping}'
```

Adopters typically deploy one function per leaf, varying
`CMDSURF_LEAF` per function (and `CMDSURF_EVENT` per event source).

## Lambda deployment (container image)

For adopters who prefer container images, see
`cmd/lambda/Dockerfile.example`. Rename to `Dockerfile`, build, push
to ECR, and create the function with `--package-type Image`. The
Dockerfile.example header has the full command sequence.

## Event-type matrix

`CMDSURF_EVENT` selects how the Lambda handler unmarshals the inbound
event and renders the bridge `Invocation`. The template root for each
event type is documented at `cmdsurface.LambdaHandler`.

| `CMDSURF_EVENT` | AWS event source           | Template root keys                    | Sample trigger |
| --------------- | -------------------------- | ------------------------------------- | -------------- |
| `apigw_v2`      | API Gateway HTTP API / Function URL | `body`, `headers`, `query`, `path` | `curl -X POST $URL -d '{"message":"hi"}'` |
| `apigw_v1`      | API Gateway REST API       | `body`, `headers`, `query`, `path`    | same as v2 with REST API URL |
| `eventbridge`   | EventBridge rule           | `detail` (decoded `event.Detail`)     | `aws events put-events --entries '[{"Source":"my.app","DetailType":"x","Detail":"{\"who\":\"alice\"}"}]'` |
| `sqs`           | SQS queue                  | `body` (per-record JSON), `headers` (flattened `MessageAttributes`) | `aws sqs send-message --queue-url $Q --message-body '{"message":"hi"}'` |
| `direct`        | service-to-service invoke  | raw `Invocation` literal              | `aws lambda invoke --function-name F --payload '{"path":["ping"]}' out.json` |

`EventDirect` skips mapping entirely — the event JSON IS the
`Invocation`. The other event types validate the leaf path eagerly at
`LambdaHandler` construction, so a misconfigured `CMDSURF_LEAF` fails
the cold start (no silent broken function).

## Cold start notes

The bridge is built once at module scope:

```go
var bridge = shared.BuildBridge()
```

That means cold start pays for:

1. Module init (Go runtime).
2. `cobra` tree construction.
3. Bridge leaf discovery + policy resolution.

In practice this is ~5ms for a tree this small. Warm invocations skip
all three — `LambdaHandler` returns a closure that reuses the existing
bridge, so the per-event cost is just template render + bridge invoke.

If your tree is large or your policy is expensive to compute,
consider:

- Splitting the tree per Lambda function (only the leaves that
  function exposes need to be built).
- Pre-computing the leaf mapping outside `init()` to avoid blocking
  the runtime's startup probe.

For Cloud Run the bridge is also built once at startup. Cloud Run
keeps the container warm between requests, so the cold-start cost
amortises across many invocations.

## Telemetry

Both legs (`cmd/lambda`, `cmd/cloudrun`) optionally wire the
**kit-telemetry** pipeline through `shared.MaybeBuildTelemetry`.
The wiring is gated on the `CMDSURFACE_DEMO_TELEMETRY` environment
variable so the cold-start path pays nothing for it by default.

Enable per leg:

| Target     | How to enable                                                        |
| ---------- | -------------------------------------------------------------------- |
| Cloud Run  | `gcloud run services update ... --set-env-vars CMDSURFACE_DEMO_TELEMETRY=1` |
| Lambda     | `aws lambda update-function-configuration ... --environment 'Variables={...,CMDSURFACE_DEMO_TELEMETRY=1}'` |

When enabled, both legs:

- Construct a dedicated `bus.Bus` for telemetry traffic.
- Install the consent FileStore (failure is non-fatal — telemetry
  stays inert until the operator runs `kit telemetry enable`).
- Build a `cmdsurface.TelemetrySink` in **ModeAnon** and wrap the
  bridge's default runner with a sink fan-out runner that pushes each
  Result through it.

The Lambda leg constructs telemetry at **module init** (the
`var bridge = ...` initialiser). This is unusual for telemetry but
unavoidable in Lambda's lifecycle — there is no `main` execution
between cold start and the first event. The Cloud Run leg constructs
in `main()` and defers `Close` on shutdown, matching the unified
example's lifecycle.

See `shared/telemetry.go` for the helper.

## Differences vs `examples/cmdsurface`

| Aspect              | `cmdsurface` (unified)    | `cmdsurface-faas` (this)            |
| ------------------- | ------------------------- | ----------------------------------- |
| Binaries            | 1                         | 2 (lambda + cloudrun)               |
| Surfaces mounted    | every surface, locally    | FaaS adapter + (cloudrun) REST/SSE/MCP |
| Lifecycle           | local signal-driven       | provider-supplied (Lambda / Cloud Run) |
| Bridge construction | per process               | per process, once, at module scope  |
| Deploy target       | dev workstation, k8s pod  | managed runtime (Lambda, Cloud Run) |
| Auth                | example middleware        | provider IAM (Lambda) / IAP (Cloud Run) |

Use the unified example to **explore** the surface matrix on your
laptop. Use this example as the template when you're ready to **ship**
a leaf or a small subset of leaves into a managed runtime.
