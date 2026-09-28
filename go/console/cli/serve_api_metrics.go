package cli

import (
	"fmt"
	"net/http"

	"hop.top/kit/go/console/cli/svcconfig"
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
// The observability provider owns and validates every key in it; the
// api service reads the two its exposure gate needs, before the
// provider starts, the way observabilityRequested reads enabled.
const (
	metricsScrapeKey         = "metrics.scrape"
	metricsScrapeEnabled     = "enabled"
	metricsScrapeAllowRemote = "allow_remote"
)

// metricsScrapeSetting returns the full key that sets the scrape key
// for the api service — its own block first, then services.all — or
// "" when neither does.
func (a *apiService) metricsScrapeSetting(key string) string {
	if a.root == nil {
		return ""
	}
	_, k, _ := svcconfig.New(a.root.Viper).Lookup(APIServiceName, metricsScrapeKey, key)
	return k
}

// metricsScrapeBool resolves one boolean scrape key, default false.
func (a *apiService) metricsScrapeBool(key string) bool {
	if k := a.metricsScrapeSetting(key); k != "" {
		return a.root.Viper.GetBool(k)
	}
	return false
}

// validateMetricsScrape is the scrape endpoint's half of the
// configuration gate. The endpoint answers ahead of the Host check and
// authentication, so beyond loopback it is refused unless the
// operator allowed it by name; a provider that serves no endpoint
// cannot honor the request at all.
func (a *apiService) validateMetricsScrape(addr string) error {
	if !a.metricsScrapeBool(metricsScrapeEnabled) {
		return nil
	}
	key := a.metricsScrapeSetting(metricsScrapeEnabled)
	if obs := a.root.serveObs; obs != nil {
		if _, ok := obs.(ServeMetricsEndpoint); !ok {
			return fmt.Errorf("%s: the linked observability provider serves no metrics endpoint", key)
		}
	}
	if isLoopbackAddr(addr) || a.metricsScrapeBool(metricsScrapeAllowRemote) {
		return nil
	}
	return fmt.Errorf(
		"%s: %q is not a loopback address and the metrics endpoint answers without authentication; "+
			"listen on 127.0.0.1, or set services.api.metrics.scrape.allow_remote: true to serve it beyond loopback",
		key, addr,
	)
}

// withMetrics puts the metrics scrape endpoint in front of h: HTTP-plane
// slot 7, beside health, answered before the Host check and
// authentication and ending the request there. routes is the bare
// router, inspected for an adopter route at the endpoint's path, which
// wins. With no provider linked, or the endpoint off, h is returned
// as is.
//
// The exposure check is repeated here against the provider's own
// resolution, so a disagreement with validateMetricsScrape fails
// closed rather than exposing the endpoint.
func (a *apiService) withMetrics(h, routes http.Handler) (http.Handler, error) {
	ep, ok := a.root.serveObs.(ServeMetricsEndpoint)
	if !ok {
		return h, nil
	}
	path, mh, allowRemote := ep.MetricsEndpoint(APIServiceName)
	if mh == nil {
		return h, nil
	}
	if addr := a.listenAddr(); !isLoopbackAddr(addr) && !allowRemote {
		return nil, fmt.Errorf("metrics endpoint: %q is not a loopback address and metrics.scrape.allow_remote is not true", addr)
	}
	return api.MetricsRoute(h, api.MetricsConfig{Path: path, Handler: mh, Routes: routes}), nil
}
