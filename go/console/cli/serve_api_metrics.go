package cli

import (
	"fmt"
	"net/http"

	"hop.top/kit/go/transport/api"
)

// ServeMetricsEndpoint is implemented by a [ServeObservability] that
// serves a metrics scrape endpoint on an HTTP service. kit's provider
// in hop.top/kit/go/transport/observability does; a provider that
// does not simply has none, and a configuration asking for one is
// refused at validation.
//
// It is a separate interface so a provider written against
// ServeObservability before it existed keeps compiling.
type ServeMetricsEndpoint interface {
	// MetricsEndpoint returns the path and handler of service's
	// scrape endpoint, and whether it may answer on a non-loopback
	// bind; "", nil and false when it is off for service. It is
	// called after Start, while the service builds its handler.
	MetricsEndpoint(service string) (path string, h http.Handler, allowRemote bool)
}

// The metrics block's scrape sub-block, services.<svc>.metrics.scrape.
// The observability provider owns and validates every key in it; an
// HTTP listener reads the two its exposure gate needs, before the
// provider starts, the way observabilityRequested reads enabled.
const (
	metricsScrapeKey         = "metrics.scrape"
	metricsScrapeEnabled     = "enabled"
	metricsScrapeAllowRemote = "allow_remote"
)

// metricsScrapeSetting returns the full key that sets the scrape key
// for the listener's service — its own block first, then
// services.all — or "" when neither does.
func (p httpPlane) metricsScrapeSetting(key string) string {
	return p.setting(metricsScrapeKey, key)
}

// metricsScrapeBool resolves one boolean scrape key, default false.
func (p httpPlane) metricsScrapeBool(key string) bool {
	if k := p.metricsScrapeSetting(key); k != "" {
		return p.root.Viper.GetBool(k)
	}
	return false
}

// validateMetricsScrape is the scrape endpoint's half of the
// configuration gate. The endpoint answers ahead of authentication, so
// beyond loopback it is refused unless the
// operator allowed it by name; a provider that serves no endpoint
// cannot honor the request at all.
func (p httpPlane) validateMetricsScrape() error {
	if !p.metricsScrapeBool(metricsScrapeEnabled) {
		return nil
	}
	key := p.metricsScrapeSetting(metricsScrapeEnabled)
	if obs := p.root.serveObs; obs != nil {
		if _, ok := obs.(ServeMetricsEndpoint); !ok {
			return fmt.Errorf("%s: the linked observability provider serves no metrics endpoint", key)
		}
	}
	addr := p.l.Addr
	if isLoopbackAddr(addr) || p.metricsScrapeBool(metricsScrapeAllowRemote) {
		return nil
	}
	return fmt.Errorf(
		"%s: %q is not a loopback address and the metrics endpoint answers without authentication; "+
			"listen on 127.0.0.1, or set %s%s.%s.%s: true to serve it beyond loopback",
		key, addr, serveKeyPrefix, p.l.Service, metricsScrapeKey, metricsScrapeAllowRemote,
	)
}

// withMetrics puts the metrics scrape endpoint in front of h, the
// router: the inner end of HTTP-plane slot 8, so the Host and Origin
// checks wrap it, answered before the router's own guards (body
// limit, compression, authentication) and ending the request there.
// Unlike the health probes (slot 7) it discloses what the service
// does — command names, surfaces, refusal counts — so a rebinding
// page must not read it, and a scraper already sends a host the check
// allows. routes is the bare router, inspected for an adopter route
// at the endpoint's path, which wins. With no provider linked, or the
// endpoint off, h is returned as is.
//
// The exposure check is repeated here against the provider's own
// resolution, so a disagreement with validateMetricsScrape fails
// closed rather than exposing the endpoint.
func (p httpPlane) withMetrics(h, routes http.Handler) (http.Handler, error) {
	ep, ok := p.root.serveObs.(ServeMetricsEndpoint)
	if !ok {
		return h, nil
	}
	path, mh, allowRemote := ep.MetricsEndpoint(p.l.Service)
	if mh == nil {
		return h, nil
	}
	if addr := p.l.Addr; !isLoopbackAddr(addr) && !allowRemote {
		return nil, fmt.Errorf("metrics endpoint: %q is not a loopback address and metrics.scrape.allow_remote is not true", addr)
	}
	return api.MetricsRoute(h, api.MetricsConfig{Path: path, Handler: mh, Routes: routes}), nil
}
