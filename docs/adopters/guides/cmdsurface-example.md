# cmdsurface example walkthrough

> Audience: adopters projecting one cobra command tree onto several
> invocation surfaces with `hop.top/kit/go/transport/cmdsurface`.

Two runnable examples share the bridge. `examples/cmdsurface` mounts
every surface in one process for local exploration;
`examples/cmdsurface-faas` ships the same bridge as two managed-runtime
binaries (AWS Lambda, Cloud Run). Each README keeps its run
instructions; this page holds the per-surface walkthroughs, the
security notes, the sink and telemetry wiring and the deployment
recipes.

## Unified binary: `examples/cmdsurface`

A single Go binary that projects one cobra command tree onto every
invocation surface kit supports — CLI, REST, ConnectRPC, MCP,
WebSocket, SSE, Bus, Cron, Library, Webhook, OAuth callback,
Signed URL — using the `hop.top/kit/go/transport/cmdsurface`
bridge. It also demonstrates the **Sink** fan-out primitive:
every Result, regardless of originating surface, flows through a
`SinkSet` for logging / auditing / replay.

Run instructions: [`examples/cmdsurface/README.md`](../../../examples/cmdsurface/README.md).

### What it shows

- One cobra tree (`widget add/list/get/delete`, `report generate/purge`,
  `subscription cancel`, `auth oauth-link`, `notify message`, `ping`,
  `tick`) defined inline.
- The bridge exposes every leaf on every surface by default, then applies
  policy: destructive leaves (`widget delete`, `report purge`) are
  allowed only on the CLI and Library surfaces; every remote surface
  refuses with `destructive_blocked` / `PERMISSION_DENIED`.
- `widget delete` is also explicitly hidden from every remote surface,
  so it appears in neither the OpenAPI spec nor the MCP `tools/list`.
- A `sinkRunner` wrapping the default `InProcessRunner` fans every
  Result through a `cmdsurface.SinkSet` — a `LogSink` (structured slog
  records) and a `FileSink` (JSON Lines to an `io.Writer`).

### Surfaces

#### CLI

```sh
go run ./examples/cmdsurface widget add --name foo --tag a
# → widget add: name=foo tags=[a]

go run ./examples/cmdsurface widget delete 42
# → widget delete: id=42  (allowed on CLI)
```

#### REST

```sh
# Happy path.
curl -sS -X POST http://localhost:8080/cmd/widget/add \
  -H 'Content-Type: application/json' \
  -d '{"flags":{"name":"foo","tag":["a","b"]}}'
# → {"exit_code":0,"stdout":"widget add: name=foo tags=[a b]\n"}

# Destructive blocked.
curl -sS -X POST http://localhost:8080/cmd/report/purge \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer test' \
  -d '{"flags":{"before":"yesterday"}}'
# → HTTP 403 / {"code":"destructive_blocked", ...}

# widget delete is Hide()n on REST → 404.
curl -sS -i -X POST http://localhost:8080/cmd/widget/delete \
  -H 'Content-Type: application/json' \
  -d '{"args":["42"]}'
# → HTTP 404

# OpenAPI spec.
curl -sS http://localhost:8080/openapi.json | jq .info.title
# → "cmdsurface example"
```

#### ConnectRPC

```sh
# Unary Invoke (Connect JSON over HTTP/1.1).
curl -sS -X POST http://localhost:8081/cmdsurface.v1.Commands/Invoke \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{"path":["widget","add"],"flags":{"name":"foo"}}'
# → {"exit_code":0,"stdout":"widget add: name=foo tags=[]\n"}
```

For typed clients, use the helpers in
[`go/transport/cmdsurface`](../../../go/transport/cmdsurface):
`connect.NewClient[Invocation, Result]` with
`cmdsurface.RPCClientOptions()` for the unary `Invoke`, and
`connect.NewClient[Invocation, Event]` for the streaming `InvokeStream`.

#### MCP

```sh
# List tools (widget.delete is absent — hidden from MCP).
curl -sS -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

# Call a tool.
curl -sS -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"widget.add","arguments":{"name":"foo"}}}'
```

#### WebSocket

The WS surface mounts at `/ws/cmd`. Frames are JSON envelopes:

```text
client → server  {"op":"invoke","id":"1","invocation":{"path":["ping"]}}
client → server  {"op":"cancel","id":"1"}
server → client  {"op":"event","id":"1","event":{...}}
server → client  {"op":"result","id":"1","result":{...}}
server → client  {"op":"error","id":"1","error":{"code":"...","message":"..."}}
```

`websocat`:

```sh
websocat ws://localhost:8080/ws/cmd
> {"op":"invoke","id":"1","invocation":{"path":["ping"]}}
< {"op":"event","id":"1","event":{"kind":"stdout","data":"pong",...}}
< {"op":"result","id":"1","result":{"exit_code":0,"stdout":"pong\n"}}
```

Streaming demo against `tick`:

```sh
websocat ws://localhost:8080/ws/cmd
> {"op":"invoke","id":"t","invocation":{"path":["tick"],"flags":{"count":3,"interval":"100ms"}}}
< {"op":"event","id":"t","event":{"kind":"stdout","data":"tick i=1",...}}
< {"op":"event","id":"t","event":{"kind":"stdout","data":"tick i=2",...}}
< {"op":"event","id":"t","event":{"kind":"stdout","data":"tick i=3",...}}
< {"op":"result","id":"t","result":{"exit_code":0,...}}
```

Cancel mid-flight:

```sh
> {"op":"invoke","id":"t","invocation":{"path":["tick"],"flags":{"count":100,"interval":"1s"}}}
> {"op":"cancel","id":"t"}
```

#### SSE

The SSE surface mounts under `/cmd` (same prefix as REST — SSE
endpoints are disambiguated by the `/stream` suffix). Query
parameters: `arg=<v>` (positional), `flag.<name>=<v>` (flags).

```sh
# Happy path.
curl -N http://localhost:8080/cmd/ping/stream
# event: event
# data: {"kind":"stdout","data":"pong",...}
#
# event: result
# data: {"exit_code":0,"stdout":"pong\n",...}

# Streaming tick.
curl -N 'http://localhost:8080/cmd/tick/stream?flag.count=3&flag.interval=100ms'

# List with a flag filter.
curl -N 'http://localhost:8080/cmd/widget/list/stream?flag.tag=foo'

# Destructive blocked (pre-stream).
curl -i -N http://localhost:8080/cmd/report/purge/stream
# → HTTP 403 / {"code":"destructive_blocked", ...}
```

#### Bus

The example wires an **in-process** `exampleBus` that satisfies both
`cmdsurface.Subscriber` and `api.EventPublisher`. It exists so the
demo is runnable in one process; real adopters substitute a Kafka /
NATS / Redis Streams subscriber + publisher and pass them to
`cmdsurface.MountBus`.

The example binds:

| Leaf         | RequestTopic       | ResponseTopic       |
| ------------ | ------------------ | ------------------- |
| `widget add` | `widgets.add.req`  | `widgets.add.resp`  |

The binding installs a subscription on `widgets.add.req`; each
message is decoded as JSON (`{"args":[...],"flags":{...},"meta":{...}}`),
invoked through the bridge with `Meta.Surface = SurfaceBus`, and the
resulting `Result` is published on `widgets.add.resp` via
`api.EventPublisher.Publish`.

Adopter sketch (Kafka):

```go
type kafkaSubscriber struct { /* sarama or franz-go client */ }
func (k *kafkaSubscriber) Subscribe(ctx context.Context, topic string,
    handler func(cmdsurface.BusMessage) error) (func(), error) { /* ... */ }

type kafkaPublisher struct { /* producer */ }
func (k *kafkaPublisher) Publish(ctx context.Context, topic, source string,
    payload any) error { /* ... */ }

cleanup, err := cmdsurface.MountBus(bridge, kSub, kPub,
    []cmdsurface.BusBinding{
        {Path: []string{"widget","add"},
         RequestTopic: "widgets.add.req",
         ResponseTopic: "widgets.add.resp"},
    })
```

#### Cron

`MountCron` schedules `report generate` to run every minute:

```go
cronEng := cmdsurface.DefaultCronEngine() // robfig/cron/v3
cleanup, err := cmdsurface.MountCron(bridge, cronEng,
    []cmdsurface.CronSchedule{
        {Path: []string{"report","generate"}, Expr: "*/1 * * * *"},
    },
    cmdsurface.WithCronLogger(func(format string, args ...any) {
        log.Printf("[cron] "+format, args...)
    }),
)
```

When the binary is running you should see one log line per minute:

```text
[cron] cmdsurface: cron report generate ran exit=0
```

Each scheduled invocation passes through the same sink pipeline as
every other surface, so it appears in the LogSink / FileSink output.

#### Library (in-process)

Adopters already in the same process can invoke leaves directly with
`InvokeArgs`:

```go
ctx := context.Background()
res, err := cmdsurface.InvokeArgs(ctx, app.Bridge, []string{"ping"})
// res.Stdout == "pong\n"
```

`StreamArgs` is the streaming counterpart and emits Events on the
caller's channel.

#### Webhook

The Webhook surface mounts at `/hooks/{name}`. The example wires one
mapping for `notify message` that verifies an HMAC-SHA256 signature
in `X-Webhook-Signature: sha256=<hex>` against the raw body, then
renders `body.source` / `body.title` into the leaf's `--source` /
`--title` flags.

```sh
# Compute signature over the exact body bytes and POST.
BODY='{"source":"github","title":"PR opened"}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac example-webhook-secret -hex | awk '{print $2}')
curl -sS -i -X POST http://localhost:8080/hooks/notify \
  -H 'Content-Type: application/json' \
  -H "X-Webhook-Signature: sha256=${SIG}" \
  -d "$BODY"
# → HTTP 202 (empty body); the leaf ran with --source=github --title="PR opened"

# Wrong signature → 401 unauthorized.
curl -sS -i -X POST http://localhost:8080/hooks/notify \
  -H 'Content-Type: application/json' \
  -H 'X-Webhook-Signature: sha256=deadbeef' \
  -d "$BODY"
# → HTTP 401 / {"code":"unauthorized", ...}

# Missing signature → 401 unauthorized.
curl -sS -i -X POST http://localhost:8080/hooks/notify \
  -H 'Content-Type: application/json' -d "$BODY"
# → HTTP 401 / {"code":"unauthorized", ...}

# Body over 1 MiB cap → 413 payload_too_large.
head -c 2097152 /dev/urandom | base64 | curl -sS -i -X POST \
  http://localhost:8080/hooks/notify \
  -H 'Content-Type: application/json' \
  -H 'X-Webhook-Signature: sha256=ignored-too-large' \
  --data-binary @-
# → HTTP 413 / {"code":"payload_too_large", ...}
```

#### OAuth callback

The OAuth surface mounts an `authorize` redirect endpoint and a
`callback` consumer. The example wires one provider (`example`)
for the `auth oauth-link` leaf.

```sh
# Step 1: visit /oauth/example/authorize → 302 to the provider's
# authorize URL with a freshly-issued state appended.
curl -sS -i http://localhost:8080/oauth/example/authorize
# → HTTP 302 Location: https://example.invalid/authorize?client_id=demo&state=<base64>

# Step 2: provider redirects back to /oauth/example/callback with
# code + state. State is single-use; expired after 2 minutes.
curl -sS -i 'http://localhost:8080/oauth/example/callback?code=abc&state=<state-from-step-1>'
# → HTTP 302 Location: /oauth-done

# Failure modes (all redirect to /oauth-error?error=<code>):
#   missing state       → error=missing_state
#   unknown state       → error=invalid_state
#   replayed state      → error=invalid_state
#   provider rejection  → error=provider_error:access_denied
```

State TTL defaults to 10 minutes; the example overrides to 2 minutes
via `WithOAuthStateTTL`. Replay (consuming a state value twice)
yields `invalid_state` regardless of TTL.

#### Signed URL

The Signed-URL surface mounts a verifier at `/x/{token}`. The
example exposes a `cmdsurface.SignedIssuer` on `exampleApp` so
in-process callers (a job worker, a notification daemon) can mint
one-shot links:

```go
issuer := app.SignedIssuer
url, err := issuer.IssueViaBridge(ctx, app.Bridge,
    cmdsurface.SignedToken{
        Path:  []string{"ping"},
        Flags: nil,
    },
    5*time.Minute,
)
// url == "/x/<base64payload>.<base64tag>"
// Caller prepends the public origin (e.g. https://app.example.com)
// to share externally.
```

Visit semantics:

- **One-shot**: the embedded nonce is recorded on first successful
  visit; subsequent visits return 401 `nonce_used`.
- **HMAC-verified**: payload tampering (any base64 segment edited)
  returns 401 `bad_signature`. Cross-key tokens (signed with key
  A, verified with key B) reject the same way.
- **TTL-bounded**: the token's `exp` field is checked against
  wall-clock; expired tokens return 401 `expired`.
- **Destructive opt-in**: by default `subscription cancel` is hidden
  from SurfaceSigned; opting in via `WithAllowDestructiveOn(SurfaceSigned)`
  is the only way to issue a URL for a destructive leaf.

### Security notes

- **Webhook HMAC defeats forgery**: every inbound request must
  carry an HMAC-SHA256 digest computed over the exact body bytes
  with the shared secret. Forgers without the secret cannot
  produce a valid signature; replaying a captured signature against
  a different body fails because the HMAC binds signature to body.
- **OAuth state defeats CSRF**: state is a 32-byte random nonce
  issued at authorize-time, persisted server-side, and atomically
  deleted on consume. An attacker cannot trick a logged-in user
  into visiting a callback with a forged code because they have
  no valid state to pair with it. Replay attempts (re-consuming a
  state) collapse to `invalid_state`.
- **Signed URL nonce defeats replay**: every signed URL carries a
  random nonce; the verifier records it on first visit and refuses
  subsequent visits. Even with a captured-but-not-yet-visited URL,
  the issuer can pre-revoke via `NonceStore.Revoke` (admin kill).

### Sinks

The example demonstrates the orthogonal **Sink** fan-out: every
Result the bridge produces — REST, RPC, MCP, WS, SSE, Bus, Cron,
Lib — passes through `cmdsurface.SinkSet`.

`cmdsurface` does NOT call sinks automatically: sinks are a fan-out
primitive, not a middleware. The example shows the idiom: a thin
`sinkRunner` wraps the inner `InProcessRunner` and dispatches each
Result to the sinks (see `sinkrunner.go`):

```go
type sinkRunner struct {
    inner cmdsurface.Runner
    sinks cmdsurface.SinkSet
}
func (s *sinkRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
    res, err := s.inner.Run(ctx, inv)
    _ = s.sinks.Emit(ctx, inv, res, err)
    return res, err
}
```

The example configures:

```go
sinks := cmdsurface.SinkSet{
    {Sink: &cmdsurface.LogSink{Handler: logger.Handler()}, OnError: true, OnOK: true},
    {Sink: &cmdsurface.FileSink{W: sinkBuf},               OnError: true, OnOK: true},
}
```

Swap `sinkBuf` (an in-memory `*bytes.Buffer`) for an `*os.File` to
get persistent JSON-Lines audit on disk; add a `WebhookSink` to
forward every invocation outcome to an external auditor:

```go
sinks = append(sinks, cmdsurface.SinkSpec{
    Sink: &cmdsurface.WebhookSink{
        URL:    "https://audit.example.com/cmd-events",
        Client: http.DefaultClient,
    },
    OnError: true, OnOK: true,
})
```

### Destructive-block contract

| Leaf            | CLI       | REST                  | RPC                       | MCP                | WS                          | SSE                          | Bus                  | Cron     |
| --------------- | --------- | --------------------- | ------------------------- | ------------------ | --------------------------- | ---------------------------- | -------------------- | -------- |
| `widget add`    | OK        | 200                   | OK                        | tools/call OK      | event+result frames         | event+result frames          | round-trip OK        | n/a      |
| `widget delete` | OK        | 404 (hidden)          | NotFound (hidden)         | absent in list     | error: unknown_command      | 404 (hidden)                 | n/a (no binding)     | n/a      |
| `report purge`  | OK        | 403 destructive_blocked | PermissionDenied        | isError + msg      | error: destructive_blocked  | 403 destructive_blocked       | error envelope       | rejected |
| `ping`          | OK        | 200                   | OK                        | tools/call OK      | event+result                | event+result                 | n/a                  | n/a      |
| `tick`          | OK        | 200                   | OK / Stream OK            | tools/call OK      | multi-event + result        | multi-event + result          | n/a                  | n/a      |

Webhook, OAuth callback and Signed URL surfaces:

| Leaf                    | Webhook                       | OAuth-CB                                 | Signed URL                                          |
| ----------------------- | ----------------------------- | ---------------------------------------- | --------------------------------------------------- |
| `notify message`        | 202 (valid sig) / 401 (bad)   | n/a (not bound)                          | issuable                                            |
| `auth oauth-link`       | n/a (not bound)               | 302 success / 302 error                  | issuable                                            |
| `subscription cancel`   | n/a (no mapping)              | n/a (no provider)                        | issue refused (destructive); opt-in via build option |
| `report purge`          | n/a (no mapping)              | n/a (no provider)                        | hidden from SurfaceSigned                            |

### Telemetry

The example optionally wires the **kit-telemetry** pipeline as an
additional Sink. The wiring is gated on the
`CMDSURFACE_DEMO_TELEMETRY` environment variable so the default
experience (`go run ./examples/cmdsurface`) pays nothing for it:

| `CMDSURFACE_DEMO_TELEMETRY` | Behaviour |
| --------------------------- | --------- |
| unset / `0` / anything else | telemetry is inert; no consent prompt, no extra bus, no extra goroutines |
| `1`                         | a `cmdsurface.TelemetrySink` is constructed, the consent FileStore is installed, and every Result fans into kit-telemetry |

When enabled, the demo defaults to **ModeAnon**. The emitter ships
only the canonical bounded fields (`command_path`, `exit_code`,
`duration_ms`, `occurred_at`, `installation_id`, `kit_version`,
`schema_version`, `sdk_lang`). Args, flags, and `_surface` stay
in-memory; they never reach the bus.

Even with the env var set, telemetry stays **inert until the operator
grants consent**. The package-level `ConsentHook` defaults to
deny-all; the FileStore lookup returns "unknown" on a fresh machine,
which collapses to `denied` at the emitter. The CLI command
`kit telemetry enable` is the one operator-facing flip that turns the
pipeline live.

#### Try it

```sh
# 1. Grant consent (one-time per machine).
kit telemetry enable

# 2. Run the demo with telemetry enabled. CLI mode bypasses the
#    bridge entirely (see main.go), so emit a sample via a surface
#    that actually goes through the bridge — e.g. the REST endpoint.
CMDSURFACE_DEMO_TELEMETRY=1 go run ./examples/cmdsurface &
sleep 1
curl -sS -X POST http://localhost:8080/cmd/ping
# → {"exit_code":0,"stdout":"pong\n"}

# 3. Inspect the captured event.
kit telemetry inspect --last 5
# → 1 event with topic "cmdsurface-demo.telemetry.event.recorded",
#   command_path=["ping"], exit_code=0, duration_ms populated.
```

#### Wiring details

The wiring lives in [`telemetry.go`](../../../examples/cmdsurface/telemetry.go). It:

1. Calls `telemetry.SetAppPrefix("cmdsurface-demo")` so the bus topic
   becomes `cmdsurface-demo.telemetry.event.recorded` (and
   `CMDSURFACE_DEMO_TELEMETRY_MODE` works as the per-app mode env).
2. Installs the consent FileStore via `consent.Install()`. Failure
   is non-fatal; default-deny stays in effect.
3. Constructs a dedicated `bus.Bus` for telemetry traffic. The demo's
   own `exampleBus` satisfies `cmdsurface.Subscriber` but not the
   canonical `bus.Bus`, so they live side by side. A real adopter
   either shares the kit bus across both or keeps them separate.
4. Builds a `telemetry.Emitter` against that bus and a
   `cmdsurface.TelemetrySink` against the emitter (`ModeAnon`).
5. Appends the sink to the existing `SinkSet` so every Bridge.Invoke
   fans into telemetry alongside the LogSink + FileSink.

Cleanup runs in `exampleApp.Cleanup` — the sink closes first (drains
queued events through the emitter), then the bus closes. A 2-second
context bounds the wait so a wedged emitter does not block shutdown.

The sink is built by direct `TelemetryOption` construction
(`NewTelemetrySink(WithEmitter(...), WithMode(...))`). Once the
`cmdsurface.Config` telemetry block lands, this can switch to a
config-driven path with no surface change to the README.

## FaaS binaries: `examples/cmdsurface-faas`

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

Local run and emulator instructions: [`examples/cmdsurface-faas/README.md`](../../../examples/cmdsurface-faas/README.md).

### What this is

`examples/cmdsurface/` is the **unified-binary** example:
a single process that mounts every surface (REST, RPC, MCP, WS, SSE,
Bus, Cron, Webhook, OAuth, Signed) locally for development. This
example is the opposite shape: **two separate binaries** built to fit
two managed-runtime contracts.

- Lambda is **single-event-handler**: one function per leaf, mapped
  via `LambdaConfig.Mapping`. Bridge constructed once at module init;
  every warm invocation reuses it.
- Cloud Run is **containerised, request-scoped HTTP**: one binary
  serving REST + SSE + MCP behind `$PORT`, with SIGTERM-driven drain.

### Cloud Run deployment

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

### Lambda deployment (zip bundle)

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

### Lambda deployment (container image)

For adopters who prefer container images, see
`cmd/lambda/Dockerfile.example`. Rename to `Dockerfile`, build, push
to ECR, and create the function with `--package-type Image`. The
Dockerfile.example header has the full command sequence.

### Event-type matrix

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

### Cold start notes

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

### Telemetry

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

### Differences vs `examples/cmdsurface`

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

## See also

- [Migrate to served commands](migrate-to-served-commands.md): replace
  manual `cmdsurface` mounting with the built-in services
- [Secure remote serving](secure-remote-serving.md): auth beyond
  loopback, one permission gate, one audit trail
- [Telemetry](telemetry.md): consent, inspect, reset, opt out
- [`go/transport/cmdsurface`](../../../go/transport/cmdsurface/README.md):
  the bridge package
