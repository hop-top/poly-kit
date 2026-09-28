package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/transport/api"
)

// ScopeName is the instrumentation scope of every span and instrument
// this package records.
const ScopeName = "hop.top/kit/go/transport/observability"

// Provider is the tracing and metrics state of one configuration: the
// tracer and meter providers it records into, and the instruments kit
// records served commands with. The zero-enabled Provider — both
// signals off — holds nothing, and every method that would wrap
// something returns what it was given.
//
// A Provider is safe for concurrent use.
type Provider struct {
	tp      trace.TracerProvider // nil when tracing is off
	mp      metric.MeterProvider // nil when metrics are off
	tracer  trace.Tracer
	inst    *instruments // nil when metrics are off
	prop    propagation.TextMapPropagator
	stops   []func(context.Context) error
	scrape  Scrape       // the scrape endpoint's configuration
	scrapeH http.Handler // nil when the scrape endpoint is off
}

// Option adjusts how [New] builds a Provider.
type Option func(*options)

type options struct {
	tp      trace.TracerProvider
	mp      metric.MeterProvider
	out     io.Writer
	name    string
	version string
}

// WithTracerProvider records spans into tp instead of building a
// provider and exporter from the configuration. It is how a tool that
// already runs an OpenTelemetry SDK, or a test with an in-memory
// recorder, supplies its own. The configuration still decides whether
// tracing is on; tp only replaces where spans go. The Provider does
// not shut tp down.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(o *options) { o.tp = tp }
}

// WithMeterProvider records metrics into mp instead of building a
// provider and exporter from the configuration. The configuration
// still decides whether metrics are on. The Provider does not shut mp
// down.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *options) { o.mp = mp }
}

// WithOutput sets where the stdout exporter writes. Default os.Stdout.
func WithOutput(w io.Writer) Option {
	return func(o *options) { o.out = w }
}

// WithServiceName names the telemetry's service.name and
// service.version resource attributes. OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES, when set, win over it.
func WithServiceName(name, version string) Option {
	return func(o *options) { o.name, o.version = name, version }
}

// exportersBuilt counts exporter constructions, so a test can prove a
// disabled configuration constructs none.
var exportersBuilt atomic.Int64

// New builds the Provider cfg describes. With both signals off it
// constructs nothing — no exporter, no provider, no instrument — and
// returns a Provider whose wrappers are all pass-through.
func New(ctx context.Context, cfg Config, opts ...Option) (*Provider, error) {
	o := options{out: os.Stdout}
	for _, fn := range opts {
		fn(&o)
	}
	p := &Provider{prop: propagation.TraceContext{}}

	var res *resource.Resource
	if (cfg.Tracing.Enabled && o.tp == nil) || (cfg.Metrics.Enabled && o.mp == nil) {
		var err error
		if res, err = newResource(ctx, o.name, o.version); err != nil {
			return nil, err
		}
	}

	switch {
	case !cfg.Tracing.Enabled:
	case o.tp != nil:
		p.tp = o.tp
	default:
		exp, err := newSpanExporter(ctx, cfg.Tracing, o.out)
		if err != nil {
			return nil, err
		}
		stp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Tracing.SampleRatio))),
		)
		p.tp = stp
		p.stops = append(p.stops, stp.Shutdown)
	}

	switch {
	case !cfg.Metrics.Enabled:
	case o.mp != nil:
		if cfg.Metrics.Scrape.Enabled {
			_ = p.Shutdown(ctx)
			return nil, errors.New("metrics.scrape needs the meter provider this package builds; " +
				"a tool that supplies its own with WithMeterProvider serves its own endpoint")
		}
		p.mp = o.mp
	default:
		mopts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		if cfg.Metrics.Exporter != ExporterNone {
			exp, err := newMetricExporter(ctx, cfg.Metrics, o.out)
			if err != nil {
				_ = p.Shutdown(ctx)
				return nil, err
			}
			mopts = append(mopts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
				sdkmetric.WithInterval(cfg.Metrics.Interval))))
		}
		if cfg.Metrics.Scrape.Enabled {
			// A second reader, cumulative and read on demand: each
			// scrape collects what every instrument has aggregated.
			reader := sdkmetric.NewManualReader()
			mopts = append(mopts, sdkmetric.WithReader(reader))
			p.scrape = cfg.Metrics.Scrape
			if p.scrape.Path == "" {
				p.scrape.Path = api.DefaultMetricsPath
			}
			p.scrapeH = scrapeHandler{reader: reader}
		}
		smp := sdkmetric.NewMeterProvider(mopts...)
		p.mp = smp
		p.stops = append(p.stops, smp.Shutdown)
	}

	if p.tp != nil {
		p.tracer = p.tp.Tracer(ScopeName)
	}
	if p.mp != nil {
		inst, err := newInstruments(p.mp.Meter(ScopeName))
		if err != nil {
			_ = p.Shutdown(ctx)
			return nil, err
		}
		p.inst = inst
	}
	return p, nil
}

// Enabled reports whether the Provider records anything.
func (p *Provider) Enabled() bool { return p != nil && (p.tp != nil || p.mp != nil) }

// Tracing reports whether the Provider records spans.
func (p *Provider) Tracing() bool { return p != nil && p.tp != nil }

// Metrics reports whether the Provider records metrics.
func (p *Provider) Metrics() bool { return p != nil && p.mp != nil }

// MetricsEndpoint returns the path and handler of the metrics scrape
// endpoint, and whether it may answer on a non-loopback bind; "", nil
// and false when the endpoint is off. The handler writes the text
// exposition of every instrument the Provider records and answers any
// method: an HTTP service mounts it with [api.MetricsRoute], which
// limits it to GET and HEAD at the path, at HTTP-plane slot 7. The
// endpoint skips authentication, so the service mounting it must
// refuse a non-loopback bind unless allowRemote is true.
func (p *Provider) MetricsEndpoint() (path string, h http.Handler, allowRemote bool) {
	if p == nil || p.scrapeH == nil {
		return "", nil, false
	}
	return p.scrape.Path, p.scrapeH, p.scrape.AllowRemote
}

// Shutdown flushes and stops the providers New built. Providers
// supplied through options are left to their owner.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	for _, stop := range p.stops {
		errs = append(errs, stop(ctx))
	}
	p.stops = nil
	return errors.Join(errs...)
}

// newResource describes the process: the tool's name and version,
// then anything the standard OTEL_* environment adds or overrides.
func newResource(ctx context.Context, name, version string) (*resource.Resource, error) {
	var attrs []attribute.KeyValue
	if name != "" {
		attrs = append(attrs, attribute.String("service.name", name))
	}
	if version != "" {
		attrs = append(attrs, attribute.String("service.version", version))
	}
	env, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, err
	}
	return resource.Merge(resource.NewSchemaless(attrs...), env)
}

// exportClient is the HTTP client the OTLP exporters use: diagnostics
// traffic, exempt from --offline like product telemetry.
func exportClient() *http.Client {
	return &http.Client{Transport: netpolicy.ObservabilityTransport(nil)}
}

// otlpEndpointFromEnv reports whether the standard environment names
// an OTLP endpoint for signal ("TRACES" or "METRICS").
func otlpEndpointFromEnv(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}

// newSpanExporter builds the configured span exporter.
func newSpanExporter(ctx context.Context, sig Signal, out io.Writer) (sdktrace.SpanExporter, error) {
	exportersBuilt.Add(1)
	if sig.Exporter == ExporterStdout {
		return stdouttrace.New(stdouttrace.WithWriter(out))
	}
	opts := []otlptracehttp.Option{otlptracehttp.WithHTTPClient(exportClient())}
	switch {
	case sig.Endpoint != "":
		opts = append(opts, otlptracehttp.WithEndpointURL(sig.Endpoint))
	case !otlpEndpointFromEnv("TRACES"):
		opts = append(opts, otlptracehttp.WithEndpointURL(DefaultEndpoint))
	}
	if len(sig.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(sig.Headers))
	}
	return otlptracehttp.New(ctx, opts...)
}

// newMetricExporter builds the configured metric exporter.
func newMetricExporter(ctx context.Context, sig Signal, out io.Writer) (sdkmetric.Exporter, error) {
	exportersBuilt.Add(1)
	if sig.Exporter == ExporterStdout {
		return stdoutmetric.New(stdoutmetric.WithWriter(out))
	}
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithHTTPClient(exportClient())}
	switch {
	case sig.Endpoint != "":
		opts = append(opts, otlpmetrichttp.WithEndpointURL(sig.Endpoint))
	case !otlpEndpointFromEnv("METRICS"):
		opts = append(opts, otlpmetrichttp.WithEndpointURL(DefaultEndpoint))
	}
	if len(sig.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(sig.Headers))
	}
	return otlpmetrichttp.New(ctx, opts...)
}
