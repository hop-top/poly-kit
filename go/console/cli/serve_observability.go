package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// ServeObservability is a tracing and metrics provider for the
// kit-shipped services (api, socket). kit ships one in
// hop.top/kit/go/transport/observability; a tool opts in by linking
// it with [WithObservability], and an operator turns it on in config.
//
// The seam is an interface over standard and kit types only, so this
// package links no OpenTelemetry code: a tool that never calls
// WithObservability carries none of it, and a tool that does pays for
// the exporters it links, not the ones it could have.
//
// The call order is fixed. `serve` calls Start once, after
// configuration resolves and before any service starts; each service
// then asks for its HTTP middleware and bridge options while it builds
// itself; the stop function Start returned runs after every service
// has stopped. A service built without a started provider (a test
// calling Start on the service directly) gets no instrumentation.
type ServeObservability interface {
	// Start resolves the tracing and metrics blocks of every named
	// service from v (the tool's configuration: services.<svc>.tracing,
	// services.<svc>.metrics, and their services.all defaults) and
	// constructs whatever they enable. name and version identify the
	// tool as the telemetry's service. A configuration error is
	// returned here, before anything binds. When nothing is enabled
	// it constructs nothing and returns a nil stop.
	Start(ctx context.Context, v *viper.Viper, name, version string, services []string) (stop func(context.Context) error, err error)
	// HTTPMiddleware returns the tracing and metrics middleware of an
	// HTTP service, for its slot in the chain. It returns nil when
	// both are off for that service.
	HTTPMiddleware(service string) func(http.Handler) http.Handler
	// BridgeOptions returns the bridge options that instrument one
	// service's invocations, or nil when nothing is enabled.
	BridgeOptions(service string) []cmdsurface.Option
}

// The configuration blocks an operator uses to turn observability on,
// under services.<svc> or services.all, and the metrics block's scrape
// endpoint sub-block. The provider owns their keys; the blocks and
// their enabled key are named here only so a tool that links no
// provider can refuse a configuration asking for one instead of
// ignoring it.
var observabilityBlocks = []string{"tracing", "metrics", metricsScrapeKey}

// observabilityStopBudget bounds the final flush of buffered spans
// and metrics once every service has stopped. It is separate from the
// shutdown budget the services draw on, because a collector that is
// down must not turn a clean shutdown into a hang.
const observabilityStopBudget = 5 * time.Second

// WithObservability links p as the tracing and metrics provider of
// every kit-shipped service. Linking enables nothing by itself: the
// operator turns tracing and metrics on per service
// (services.api.tracing.enabled) or for all of them
// (services.all.metrics.enabled), and both default to off. Trace
// context is propagated either way; only export needs a provider.
//
// With a provider linked and enabled, the api service's middleware
// chain gains the tracing and metrics middleware at its slot (a span
// and HTTP metrics per request, refusals by later middleware
// included), and the bridges of the api and socket services — and of
// any service built with [ServeBridgeOptions], such as mcp and rpc —
// gain a span per invocation plus request, latency, in-flight and
// refusal metrics labeled by service and surface. With
// services.api.metrics.scrape enabled as well, the api service
// answers a Prometheus scrape at /metrics (see [ServeMetricsEndpoint]).
func WithObservability(p ServeObservability) func(*Root) {
	return func(r *Root) { r.serveObs = p }
}

// startObservability starts the linked provider for one serve run
// and returns the flush to run once the services have stopped. A tool
// with no provider gets a no-op, unless its configuration enables
// traces or metrics: that is refused as a usage error, since serving
// without the telemetry the operator asked for is a silent failure.
func (r *Root) startObservability(ctx context.Context, services []string) (func(), error) {
	noop := func() {}
	if r.serveObs == nil {
		if key := observabilityRequested(r.Viper, services); key != "" {
			e := output.UsageError(fmt.Sprintf(
				"%s is set but this tool links no observability provider", key))
			e.SuggestedFix = "unset it, or have the tool call cli.WithObservability"
			return noop, e
		}
		return noop, nil
	}
	stop, err := r.serveObs.Start(ctx, r.Viper, r.Config.Name, r.Config.Version, services)
	if err != nil {
		return noop, output.UsageError(fmt.Sprintf("observability: %v", err))
	}
	if stop == nil {
		return noop, nil
	}
	return func() {
		// The serve context is canceled by now; the flush needs a
		// live one of its own.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observabilityStopBudget)
		defer cancel()
		_ = stop(fctx)
	}, nil
}

// observabilityRequested returns the first services.<svc>.<block>.enabled
// key v sets to true, for services and services.all, or "".
func observabilityRequested(v *viper.Viper, services []string) string {
	if v == nil {
		return ""
	}
	for _, svc := range append([]string{serveAllScope}, services...) {
		for _, block := range observabilityBlocks {
			key := serveKeyPrefix + svc + "." + block + serveSubkeyEnabled
			if v.GetBool(key) {
				return key
			}
		}
	}
	return ""
}

// observeMiddleware returns the linked provider's tracing and metrics
// middleware for service, or a pass-through when there is none, so it
// can stand in a middleware list unconditionally.
func (r *Root) observeMiddleware(service string) func(http.Handler) http.Handler {
	if r != nil && r.serveObs != nil {
		if mw := r.serveObs.HTTPMiddleware(service); mw != nil {
			return mw
		}
	}
	return func(h http.Handler) http.Handler { return h }
}

// serveObservabilityOptions returns the bridge options that
// instrument service's invocations, or nil.
func (r *Root) serveObservabilityOptions(service string) []cmdsurface.Option {
	if r == nil || r.serveObs == nil {
		return nil
	}
	return r.serveObs.BridgeOptions(service)
}
