# Served-command observability reference

Tracing and metrics for commands served over the api, socket and RPC
surfaces: which trace context kit propagates without any setup, what
[`go/transport/observability`](../../../go/transport/observability/README.md)
records once linked and enabled, the configuration keys, and the
instrument and span names. The walkthrough is step 9 of
[secure-remote-serving.md](../guides/secure-remote-serving.md#9-trace-and-measure-served-commands).

This is operational telemetry about your service: OpenTelemetry spans
and metrics sent to a collector you run. It is unrelated to kit's
product telemetry ([telemetry.md](telemetry.md)), which is
consent-gated usage reporting.

## Two levels

| Level | Needs | What you get |
|---|---|---|
| Propagation | nothing; always on | the caller's W3C `traceparent` / `tracestate` reach `Meta.Traceparent` / `Meta.Tracestate` on REST and RPC, the audit sinks, and a subprocess's `TRACEPARENT` / `TRACESTATE` |
| Export | the tool links the provider; the operator enables it | spans per request and per invocation, request / latency / in-flight / refusal metrics, exported over OTLP or to stdout |

Nothing is exported by default, anywhere. Linking the provider
enables nothing; configuration does.

The built-in `mcp` and `rpc` services get the per-invocation spans
and metrics through their bridges, under `services.mcp.*` and
`services.rpc.*`; their own listeners carry no HTTP request spans or
metrics yet.

## Linking the provider

```go
import (
    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/observability"
)

root := cli.New(cfg,
    cli.WithAPI(apiCfg),
    cli.WithSocket(socketCfg),
    cli.WithObservability(observability.NewServe()),
)
```

`go/console/cli`, `go/transport/api` and `go/transport/cmdsurface`
import no OpenTelemetry code. The SDK and the exporters reach a binary
only through this option, so a tool that does not call it carries none
of them.

A tool with no provider linked refuses a configuration that enables
tracing or metrics, at exit `2`, naming the key; it never serves
silently without the telemetry the operator asked for.

`NewServe` accepts options for tools that already run an
OpenTelemetry SDK, or tests:

| Option | Effect |
|---|---|
| `observability.WithTracerProvider(tp)` | record spans into `tp` instead of building an exporter; configuration still decides whether tracing is on |
| `observability.WithMeterProvider(mp)` | record metrics into `mp`, likewise |
| `observability.WithOutput(w)` | where the `stdout` exporter writes (default `os.Stdout`) |

## Configuration

Two blocks per service, `tracing` and `metrics`, each with its own
`enabled`. Set them per service, or once for every service under
`services.all`:

```yaml
services:
  all:
    tracing:
      enabled: true
      exporter: otlp                  # otlp (default) or stdout
      endpoint: http://127.0.0.1:4318
  api:
    metrics:
      enabled: true
      interval: 30s
  socket:
    tracing:
      enabled: false                  # overrides services.all for the socket
```

Resolution is per key: `services.<svc>.<block>.<key>`, then
`services.all.<block>.<key>`, then the default. Setting one key under a
service keeps the others from `services.all`.

| Key | Blocks | Default | Meaning |
|---|---|---|---|
| `enabled` | both | `false` | export this signal for this service |
| `exporter` | both | `otlp` | `otlp` (OTLP over HTTP, protobuf) or `stdout` (JSON lines) |
| `endpoint` | both | `http://127.0.0.1:4318` | OTLP base URL. When unset, `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_{TRACES,METRICS}_ENDPOINT` wins over the loopback default |
| `headers` | both | none | map of headers sent with every OTLP request (a collector token) |
| `sample_ratio` | `tracing` | `1` | fraction of new traces recorded; a caller's sampled parent is always followed |
| `interval` | `metrics` | `60s` | metrics export period |

- An unknown key inside a `tracing` or `metrics` block, an unknown
  exporter, a ratio outside `[0, 1]` or a non-positive interval is a
  usage error at `serve` start, exit `2`.
- Services whose resolved configuration is identical share one
  exporter per signal.
- The default endpoint is loopback: enabling export never leaves the
  host until an endpoint says so. Export traffic is diagnostics, so
  `--offline` does not mute it (the same carve-out product telemetry
  has); the OTLP client carries no user traffic.
- The resource's `service.name` and `service.version` are the tool's
  name and version; `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`
  override them.
- Environment variables follow the kit convention, e.g.
  `MYTOOL_SERVICES_ALL_TRACING_ENABLED=true`.

## Spans

One trace per call, from the caller down to any child process:

| Span | Kind | Parent | Started by |
|---|---|---|---|
| `<METHOD> <route>` (e.g. `GET /v1/commands/item/list`) | server | the caller's `traceparent` | HTTP middleware (otelhttp), api service |
| `<service>/<Procedure>` (e.g. `cmdsurface.v1.Commands/Invoke`) | server | the caller's `traceparent` | RPC interceptor (otelconnect) |
| `invoke <command path>` | internal; server when no request span exists (socket) | the request span, else `Meta.Traceparent` | bridge runner middleware |
| whatever the command records | — | the invocation span | the command, via `trace.SpanFromContext(cmd.Context())` |
| whatever a child process records | — | the invocation span | a traced child reading `TRACEPARENT` |

Invocation span attributes: `kit.service`, `kit.surface`,
`kit.command`, `kit.request_id`, `kit.exit_code`. A failed invocation
(error or non-zero exit) has status `Error`. A refusal adds a
`kit.refusal` event carrying `kit.refusal.reason` to the request span;
a refused call has no invocation span, because it never reached a
runner.

## Metrics

Invocation plane, labeled `kit.service` and `kit.surface`:

| Instrument | Type | Unit | Extra attributes |
|---|---|---|---|
| `kit.serve.requests` | counter | `{request}` | `kit.outcome` (`ok`, `error`, `refused`), `kit.refusal.reason` when refused, `kit.command` when the path resolved |
| `kit.serve.request.duration` | histogram | `s` | as above; receipt to verdict, refusals included |
| `kit.serve.requests.active` | up-down counter | `{request}` | invocations running now, past every gate |
| `kit.serve.refusals` | counter | `{refusal}` | `kit.refusal.reason` |

HTTP plane, labeled `kit.service`:

| Instrument | Type | Meaning |
|---|---|---|
| `http.server.*` | per otelhttp | request duration and sizes, by method and status |
| `kit.serve.http.requests.active` | up-down counter | HTTP requests in progress |
| `kit.serve.http.refusals` | counter | requests refused by HTTP-plane middleware, by `kit.refusal.reason` |
| `rpc.server.*` | per otelconnect | RPC duration and sizes, when the RPC interceptor is wired |

An unresolved command path never becomes a label: a caller cannot mint
series by requesting commands that do not exist.

### Refusal codes

`kit.refusal.reason` carries the stable refusal code:

| Code | Counted in | From |
|---|---|---|
| `unknown_command`, `not_enabled`, `not_invocable`, `destructive_blocked`, `permission_denied` | `kit.serve.refusals` | the bridge's gates |
| `unauthenticated` | `kit.serve.refusals` | the transport edge, reported through `Bridge.Audit` |
| `deadline_exceeded` | `kit.serve.refusals` | an invocation whose context deadline passed |
| `body_too_large` | `kit.serve.http.refusals` | `api.BodyLimit`: a declared length over the cap, or a body of unknown length crossing it |
| `host_rejected`, `origin_rejected` | `kit.serve.http.refusals` | `api.HostCheck`, `api.OriginCheck` |
| any other HTTP-plane code | `kit.serve.http.refusals` | middleware calling `api.RecordRefusal(r, code)` |

HTTP-plane middleware that refuses a request calls
`api.RecordRefusal(r, code)` before writing its response; the tracing
and metrics middleware, which sits outside it, counts the code. A
refusal the bridge decides is counted once, on the invocation plane.

## Chain position

On the api service's HTTP chain the middleware sits at slot 5 of the
[serve-lifecycle contract](../../contracts/serve-lifecycle.md): after
request id, client address, access log and recovery, before security
headers and everything that can refuse, so every refusal below it is
measured. It wraps the router rather than sitting in it, so health
probes and requests refused before routing get a span too, named
`<METHOD>` alone; a routed request's span is renamed
`<METHOD> <route>` once the router has matched. On the RPC server it is the tracing interceptor. On the
bridge it is a runner middleware (innermost, after every gate) plus an
audit sink (every verdict).

## Wiring without the CLI

`observability.New(ctx, cfg, opts...)` builds a `*Provider` from a
`Config` directly. Its methods instrument what you mount yourself:

| Method | Use |
|---|---|
| `HTTPMiddleware(service)` | an `api.Middleware` for your router; nil when off |
| `RPCInterceptor()` | a `connect.Interceptor` for `rpc.WithInterceptors` or `cmdsurface.WithRPCInterceptors`; nil when off |
| `BridgeOptions(service)` | `cmdsurface.New` options: the runner middleware and the verdict sink; nil when off |
| `Shutdown(ctx)` | flush and stop the exporters it built |

Under the CLI, an adopter's own service reaches the same Provider with
`serve.Provider(name)` from its `Start`, which runs after the provider
started.

## Propagation details

- REST and RPC read `traceparent` and `tracestate`. A `traceparent`
  reaches `Meta.Traceparent` only when it is well-formed per W3C
  (lowercase hex, non-zero ids, version not `ff`); `tracestate` only
  beside it. `Meta.TraceID` keeps its older, lenient extraction.
- Over RPC the message's own `meta.trace_id` stays authoritative for
  `Meta.TraceID`; the headers supply `Meta.Traceparent`.
- `SubprocessRunner` sets `TRACEPARENT` and `TRACESTATE` in the child's
  environment from `Meta`, replacing any the server inherited. With
  tracing enabled, `Meta.Traceparent` is the invocation span's own
  context, so the child is its child. Without one, the environment
  passes through untouched.
- An in-process command's context carries the invocation span.

## Not implemented

- A Prometheus `/metrics` endpoint. Metrics are pushed (OTLP) or
  printed (`stdout`); a collector turns OTLP into Prometheus where one
  is wanted. The contract reserves slot 7 for such an endpoint.
- A queued-invocations gauge: there is no admission queue yet.
- Trace context on the socket wire and MCP `_meta`. The socket carries
  `trace_id` only, so a socket invocation span starts a new trace; MCP
  over HTTP is traced by the HTTP middleware.

## Related pages

- [cmdsurface.md](cmdsurface.md) — the bridge, runners and sinks
- [transport-api.md](transport-api.md) — request provenance and the
  api middleware
- [secure-remote-serving.md](../guides/secure-remote-serving.md) — the
  serving guide this extends
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md) — the
  middleware order and configuration rules
