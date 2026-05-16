# cmdsurface example

A single Go binary that projects one cobra command tree onto every
invocation surface kit supports — CLI, REST, ConnectRPC, MCP,
WebSocket, SSE, Bus, Cron, Library, Webhook, OAuth callback,
Signed URL — using the `hop.top/kit/go/transport/cmdsurface`
bridge. It also demonstrates the **Sink** fan-out primitive:
every Result, regardless of originating surface, flows through a
`SinkSet` for logging / auditing / replay.

## What it shows

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

## Run

```sh
# Start the servers (REST + MCP + WS + SSE on :8080, RPC on :8081).
go run ./examples/cmdsurface

# Invoke the same tree locally — arguments after the program name
# switch into CLI mode.
go run ./examples/cmdsurface widget add --name foo --tag a --tag b
go run ./examples/cmdsurface tick --count 3 --interval 100ms
```

OpenAPI spec: <http://localhost:8080/openapi.json>

## Surfaces

### CLI

```sh
go run ./examples/cmdsurface widget add --name foo --tag a
# → widget add: name=foo tags=[a]

go run ./examples/cmdsurface widget delete 42
# → widget delete: id=42  (allowed on CLI)
```

### REST

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

### ConnectRPC

```sh
# Unary Invoke (Connect JSON over HTTP/1.1).
curl -sS -X POST http://localhost:8081/cmdsurface.v1.Commands/Invoke \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{"path":["widget","add"],"flags":{"name":"foo"}}'
# → {"exit_code":0,"stdout":"widget add: name=foo tags=[]\n"}
```

For typed clients, use the helpers in
[`go/transport/cmdsurface`](../../go/transport/cmdsurface):
`connect.NewClient[Invocation, Result]` with
`cmdsurface.RPCClientOptions()` for the unary `Invoke`, and
`connect.NewClient[Invocation, Event]` for the streaming `InvokeStream`.

### MCP

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

### WebSocket

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

### SSE

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

### Bus

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

### Cron

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

```
[cron] cmdsurface: cron report generate ran exit=0
```

Each scheduled invocation passes through the same sink pipeline as
every other surface, so it appears in the LogSink / FileSink output.

### Library (in-process)

Adopters already in the same process can invoke leaves directly with
`InvokeArgs`:

```go
ctx := context.Background()
res, err := cmdsurface.InvokeArgs(ctx, app.Bridge, []string{"ping"})
// res.Stdout == "pong\n"
```

`StreamArgs` is the streaming counterpart and emits Events on the
caller's channel.

### Webhook

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

### OAuth callback

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

### Signed URL

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

## Security notes

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

## Sinks

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

## Destructive-block contract

| Leaf            | CLI       | REST                  | RPC                       | MCP                | WS                          | SSE                          | Bus                  | Cron     |
| --------------- | --------- | --------------------- | ------------------------- | ------------------ | --------------------------- | ---------------------------- | -------------------- | -------- |
| `widget add`    | OK        | 200                   | OK                        | tools/call OK      | event+result frames         | event+result frames          | round-trip OK        | n/a      |
| `widget delete` | OK        | 404 (hidden)          | NotFound (hidden)         | absent in list     | error: unknown_command      | 404 (hidden)                 | n/a (no binding)     | n/a      |
| `report purge`  | OK        | 403 destructive_blocked | PermissionDenied        | isError + msg      | error: destructive_blocked  | 403 destructive_blocked       | error envelope       | rejected |
| `ping`          | OK        | 200                   | OK                        | tools/call OK      | event+result                | event+result                 | n/a                  | n/a      |
| `tick`          | OK        | 200                   | OK / Stream OK            | tools/call OK      | multi-event + result        | multi-event + result          | n/a                  | n/a      |

Wave 3 surfaces:

| Leaf                    | Webhook                       | OAuth-CB                                 | Signed URL                                          |
| ----------------------- | ----------------------------- | ---------------------------------------- | --------------------------------------------------- |
| `notify message`        | 202 (valid sig) / 401 (bad)   | n/a (not bound)                          | issuable                                            |
| `auth oauth-link`       | n/a (not bound)               | 302 success / 302 error                  | issuable                                            |
| `subscription cancel`   | n/a (no mapping)              | n/a (no provider)                        | issue refused (destructive); opt-in via build option |
| `report purge`          | n/a (no mapping)              | n/a (no provider)                        | hidden from SurfaceSigned                            |

## End-to-end tests

```sh
go test -tags=e2e -race -count=1 ./examples/cmdsurface/...
```
