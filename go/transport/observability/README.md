# observability

## What it answers

How a served command tree is traced and measured with OpenTelemetry:
one trace from the caller's `traceparent` through the HTTP or RPC
request and the command invocation into any child process, and
request, latency, in-flight, queued and refusal metrics per service and
surface. Off until configured; linked only by tools that ask for it.

## Use it when

- trace and measure the kit-shipped `api` and `socket` services → `cli.WithObservability(observability.NewServe())`, then `services.<svc>.tracing.enabled` / `services.<svc>.metrics.enabled` (or `services.all.*`) in config
- send to a collector → `exporter: otlp` (default), `endpoint: http://127.0.0.1:4318` (default); print locally → `exporter: stdout`
- let Prometheus scrape a kit HTTP listener (api, mcp, rpc) → `metrics.scrape.enabled: true` (`/metrics`; `exporter: none` to push nothing; `scrape.allow_remote: true` beyond loopback)
- keep your own OpenTelemetry SDK → `NewServe(WithTracerProvider(tp), WithMeterProvider(mp))`
- instrument a router, RPC server or bridge you mount yourself → `New(ctx, cfg)`, then `HTTPMiddleware`, `RPCInterceptor`, `BridgeOptions`
- count an HTTP-plane refusal by code from your own middleware → `api.RecordRefusal(r, code)`

## Quick start

```go
root := cli.New(cfg,
    cli.WithAPI(apiCfg),
    cli.WithObservability(observability.NewServe()),
)
```

```yaml
services:
  all:
    tracing:
      enabled: true
    metrics:
      enabled: true
```

## Contract

- Nothing is constructed while both signals are off for a service: no exporter, no provider, no middleware, no bridge option.
- Configuration resolves per key: `services.<svc>.<block>.<key>`, then `services.all.<block>.<key>`, then the default. An unknown key, exporter or out-of-range value fails `serve` at start, exit `2`.
- The default OTLP endpoint is loopback; `OTEL_EXPORTER_OTLP_*` endpoints win over it when the config sets none. Export uses `netpolicy.ObservabilityTransport`.
- The request span continues the caller's `traceparent`; the invocation span is its child, or the child of `Meta.Traceparent` when no request span exists; the runner receives the invocation span's context in `Meta`, and `SubprocessRunner` passes it on as `TRACEPARENT`.
- Every verdict the bridge emits is counted and timed; refusals carry their stable code. An unresolved command path is never a metric label.
- Providers supplied through options are never shut down by this package.
- The scrape endpoint is a text-format encoder over an SDK `ManualReader`, not the upstream Prometheus exporter, so linking this package pulls in no Prometheus client library. It needs the meter provider this package builds.

## Neighbours

- `go/transport/cmdsurface`: `WithRunnerMiddleware` and sinks are the seams used on the invocation plane.
- `go/transport/api`: `TraceContextFromHeader`, `RecordRefusal` / `ObserveRefusal`; imports no OpenTelemetry.
- `go/console/cli`: `WithObservability` and the `ServeObservability` seam; imports no OpenTelemetry.
- `go/runtime/telemetry`: consent-gated product telemetry, a different concern.

## See also

- [served-observability reference](../../../docs/adopters/reference/served-observability.md): keys, spans, instruments, refusal codes
- [secure-remote-serving.md](../../../docs/adopters/guides/secure-remote-serving.md#9-trace-and-measure-served-commands): the walkthrough
