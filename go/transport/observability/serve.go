package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"connectrpc.com/connect"
	"github.com/spf13/viper"

	"hop.top/kit/go/transport/cmdsurface"
)

// Serve is the provider the kit CLI's served services report to. Link
// it with cli.WithObservability(observability.NewServe()); `serve`
// then starts it with the tool's configuration and every kit-shipped
// service picks up its instrumentation.
//
// Each service resolves its own tracing and metrics blocks, so an
// operator can trace the api service and not the socket. Services
// whose resolved configuration is identical share one Provider, and
// so one exporter per signal.
type Serve struct {
	opts []Option

	mu        sync.Mutex
	byService map[string]*Provider
	providers []*Provider
}

// NewServe returns a Serve that builds its Providers with opts (a
// tracer or meter provider of the tool's own, the stdout exporter's
// writer).
func NewServe(opts ...Option) *Serve {
	return &Serve{opts: opts}
}

// Start resolves every named service's configuration from v and builds
// the Providers it enables. An unknown key or an invalid value in any
// tracing or metrics block is an error, returned before any service
// binds. It returns a nil stop when nothing is enabled.
func (s *Serve) Start(ctx context.Context, v *viper.Viper, name, version string, services []string) (func(context.Context) error, error) {
	if err := Validate(v); err != nil {
		return nil, err
	}
	type built struct {
		cfg Config
		p   *Provider
	}
	var shared []built
	byService := map[string]*Provider{}
	var providers []*Provider
	opts := append([]Option{WithServiceName(name, version)}, s.opts...)

	for _, svc := range services {
		cfg, err := Resolve(v, svc)
		if err != nil {
			return nil, errors.Join(err, shutdownAll(ctx, providers))
		}
		if !cfg.Enabled() {
			continue
		}
		var p *Provider
		for _, b := range shared {
			if reflect.DeepEqual(b.cfg, cfg) {
				p = b.p
				break
			}
		}
		if p == nil {
			if p, err = New(ctx, cfg, opts...); err != nil {
				return nil, errors.Join(fmt.Errorf("%s: %w", svc, err), shutdownAll(ctx, providers))
			}
			shared = append(shared, built{cfg: cfg, p: p})
			providers = append(providers, p)
		}
		byService[svc] = p
	}

	s.mu.Lock()
	s.byService, s.providers = byService, providers
	s.mu.Unlock()
	if len(providers) == 0 {
		return nil, nil
	}
	return func(ctx context.Context) error { return shutdownAll(ctx, providers) }, nil
}

// Provider returns the Provider instrumenting service, or nil when
// tracing and metrics are both off for it (or Start has not run). An
// adopter's own service uses it to instrument what kit does not wire:
// an RPC server's interceptor, its own HTTP listener.
func (s *Serve) Provider(service string) *Provider {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byService[service]
}

// HTTPMiddleware returns service's tracing and metrics middleware, or
// nil when both are off for it.
func (s *Serve) HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return s.Provider(service).HTTPMiddleware(service)
}

// MetricsEndpoint returns the path and handler of service's metrics
// scrape endpoint and whether it may answer beyond loopback, or "",
// nil and false when it is off for service (see
// [Provider.MetricsEndpoint]). Services whose configuration is
// identical share a Provider, so one endpoint then reports all of
// them, each series labeled kit.service.
func (s *Serve) MetricsEndpoint(service string) (path string, h http.Handler, allowRemote bool) {
	return s.Provider(service).MetricsEndpoint()
}

// BridgeOptions returns the bridge options that instrument service's
// invocations, or nil when nothing is enabled for it.
func (s *Serve) BridgeOptions(service string) []cmdsurface.Option {
	return s.Provider(service).BridgeOptions(service)
}

// RPCInterceptor returns service's RPC tracing and metrics
// interceptor, or nil when both are off for it. Call it once Start has
// run: from a service's own Start, which the supervisor calls after.
func (s *Serve) RPCInterceptor(service string) (connect.Interceptor, error) {
	return s.Provider(service).RPCInterceptor()
}

func shutdownAll(ctx context.Context, providers []*Provider) error {
	var errs []error
	for _, p := range providers {
		errs = append(errs, p.Shutdown(ctx))
	}
	return errors.Join(errs...)
}
