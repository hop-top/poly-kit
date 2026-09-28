package api

import "net/http"

// DefaultMetricsPath is where [MetricsRoute] answers when
// [MetricsConfig.Path] is empty: the path Prometheus scrapes by
// default.
const DefaultMetricsPath = "/metrics"

// MetricsConfig configures [MetricsRoute].
type MetricsConfig struct {
	// Path is where the endpoint answers. Empty means
	// [DefaultMetricsPath]. It must be an absolute, clean URL path;
	// the caller validates it.
	Path string
	// Handler writes the exposition. MetricsRoute sends it GET and
	// HEAD requests at Path only. Nil means there is no endpoint, and
	// MetricsRoute returns next unchanged.
	Handler http.Handler
	// Routes is the handler inspected for an adopter route at Path,
	// as [HealthConfig.Routes]. Nil means next itself.
	Routes http.Handler
}

// MetricsRoute returns a handler that answers the metrics scrape
// endpoint with cfg.Handler and passes every other request to next.
//
// A scraper carries no credentials, so the endpoint is answered BEFORE
// next and ends the request there: nothing next applies — body
// limits, compression, authentication — sees a scrape. Middleware
// that must cover it too wraps the handler MetricsRoute returns. Unlike
// [HealthRoutes], that includes the Host and Origin checks: the
// exposition discloses what the service does, so a DNS-rebinding page
// must not read it, and a scraper already sends a host the check
// allows. kit's api service mounts it inside those checks, below the
// request id, access log, recovery, telemetry and security headers.
// Because it skips authentication, whoever mounts it decides where it
// may be reached; kit's api service refuses a non-loopback bind unless
// the operator allows it.
//
// A route the adopter registered at exactly cfg.Path wins, as it does
// for the health routes. The endpoint answers GET and HEAD, never
// caches, and is not registered on the router, so it appears in
// neither the capabilities listing, the command discovery document,
// nor the OpenAPI description.
func MetricsRoute(next http.Handler, cfg MetricsConfig) http.Handler {
	if cfg.Handler == nil {
		return next
	}
	p := cfg.Path
	if p == "" {
		p = DefaultMetricsPath
	}
	routes := cfg.Routes
	if routes == nil {
		routes = next
	}
	if servesExactly(routes, p) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != p {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			Error(w, http.StatusMethodNotAllowed, &APIError{
				Status:  http.StatusMethodNotAllowed,
				Code:    "method_not_allowed",
				Message: "the metrics endpoint answers GET and HEAD",
			})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		cfg.Handler.ServeHTTP(w, r)
	})
}
