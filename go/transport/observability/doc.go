// Package observability traces and measures served commands with
// OpenTelemetry: one trace from the caller's traceparent through the
// HTTP or RPC request, the command invocation, and any child process
// the command runs; and request, latency, in-flight and refusal
// metrics per service and surface.
//
// It is opt-in twice over. A tool links it, which is the only way its
// OpenTelemetry SDK and exporters reach a binary: the transport and
// cli packages every tool imports carry none of it. An operator then
// enables traces and metrics in configuration; both default to off,
// and with both off nothing is constructed — no exporter, no provider,
// no wrapper — so a linked but disabled provider costs nothing per
// request.
//
// # Wiring
//
// With the kit CLI, one option links it and configuration does the
// rest:
//
//	root := cli.New(cfg, cli.WithAPI(apiCfg), cli.WithObservability(observability.NewServe()))
//
//	services:
//	  all:                          # every service; services.<svc> overrides per key
//	    tracing:
//	      enabled: true             # default false
//	      exporter: otlp            # otlp (default) or stdout
//	      endpoint: http://127.0.0.1:4318
//	  api:
//	    metrics:
//	      enabled: true             # default false
//	      interval: 60s
//	      scrape:
//	        enabled: true           # GET /metrics on the api service; default false
//
// Without it, [New] builds a [Provider] from a [Config] (or from
// providers the tool already owns, via [WithTracerProvider] and
// [WithMeterProvider]) and its methods wrap an HTTP handler, a
// ConnectRPC handler and a cmdsurface bridge directly.
//
// # Local first
//
// Nothing leaves the host unless configured to. The OTLP exporters
// default to a collector on loopback (http://127.0.0.1:4318) unless the
// configuration or the standard OTEL_EXPORTER_OTLP_* environment names
// another endpoint, and the stdout exporter writes locally. Export
// traffic is diagnostics, so it rides
// [hop.top/kit/go/core/netpolicy.ObservabilityTransport] and is not
// muted by --offline, the same carve-out product telemetry has.
//
// # Scrape endpoint
//
// With metrics.scrape enabled, the api service answers a Prometheus
// scrape (text exposition format) at /metrics, at the inner end of
// HTTP-plane slot 8: after the Host check, which a scraper passes by
// sending an allowed host, and ahead of authentication, so a
// non-loopback bind needs metrics.scrape.allow_remote. Exporter "none" pushes nothing, for a
// service read by scraping alone. See [Provider.MetricsEndpoint].
//
// # What is recorded
//
// Spans: the HTTP server span (otelhttp) or RPC server span
// (otelconnect), parented on the caller's W3C traceparent; below it an
// "invoke <command>" span per invocation that passed the bridge's
// gates; below that, whatever the command and its child processes
// record, since the invocation's traceparent reaches the runner in
// Invocation.Meta and a subprocess as TRACEPARENT. Refusals add a
// "kit.refusal" event to the request span.
//
// Metrics, labeled kit.service and kit.surface:
//
//   - kit.serve.requests: every verdict, with kit.outcome (ok, error,
//     refused) and, when refused, kit.refusal.reason.
//   - kit.serve.request.duration: seconds from receipt to verdict,
//     refusals included.
//   - kit.serve.requests.active: invocations running now.
//   - kit.serve.refusals: refusals by kit.refusal.reason.
//   - kit.serve.http.requests.active: HTTP requests in progress,
//     beside the http.server.* instruments otelhttp records.
//   - kit.serve.http.refusals: requests refused by HTTP-plane
//     middleware, by the code it recorded with api.RecordRefusal.
package observability
