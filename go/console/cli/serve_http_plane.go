package cli

import (
	"net/http"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/transport/api"
)

// ServeHTTPListener describes one kit HTTP listener to the HTTP-plane
// middleware chain (serve-lifecycle.md §"Middleware order on the HTTP
// plane"): which service's blocks configure it, where it listens, and
// how its protocol writes a refusal. The api service's router, the
// rpc server and the mcp service's HTTP transport are each one.
type ServeHTTPListener struct {
	// Service names the services.<Service>.* blocks the chain reads,
	// with services.all as their shared default.
	Service string
	// Addr is the configured listen address. The Host allowlist is
	// derived from it, and health.detail and the metrics endpoint's
	// exposure default follow whether it is loopback.
	Addr string
	// Ready is the service's own readiness, the first check /readyz
	// reports. Nil reports ready.
	Ready func() bool
	// Checks are further readiness inputs, after Ready.
	Checks []api.ReadinessCheck
	// Routes is the handler inspected for an adopter route at a probe
	// or scrape path, which wins over kit's (see
	// [api.HealthConfig.Routes]). Nil means no adopter routes.
	Routes http.Handler
	// MaxBodyBytes is the service's code option for body_limit, which
	// services.<svc>.body_limit overrides: zero is
	// [api.DefaultMaxBodyBytes], negative is no cap.
	MaxBodyBytes int64
	// OnBodyTooLarge observes each body refusal, for an audit trail.
	OnBodyTooLarge func(r *http.Request, limit int64)
	// Refuse writes the refusals decided on the HTTP plane — Host,
	// Origin, body limit — in the listener's protocol. Nil writes an
	// [api.APIError].
	Refuse api.RefusalWriter
}

// ServeHTTPSettings is the resolved configuration of the HTTP-plane
// blocks a listener's protocol layer enforces a copy of: the MCP
// SDK's body cap and DNS-rebinding check, Connect's read limit and
// per-message compression. A listener sets that copy from these, so
// the protocol layer never refuses what the service's configuration
// allows, nor allows what it refuses.
type ServeHTTPSettings struct {
	// MaxBodyBytes is the request body cap; negative means none.
	MaxBodyBytes int64
	// HostCheck is host_check.enabled.
	HostCheck bool
	// AllowHosts is host_check.allow: hosts beyond the listener's own.
	AllowHosts []string
	// Compression is compression.enabled.
	Compression bool
	// CompressMinBytes is compression.min_bytes.
	CompressMinBytes int
}

// ValidateServeHTTPListener is the configuration gate of the
// HTTP-plane blocks for listener l: an unknown key in one of them, a
// value that does not parse, a health path prefix no probe is pointed
// at, or a metrics endpoint beyond loopback without its opt-in, is an
// error before anything binds. A service calls it from its Validate
// hook.
func ValidateServeHTTPListener(r *Root, l ServeHTTPListener) error {
	return httpPlane{root: r, l: l}.validate()
}

// ResolveServeHTTPListener resolves the blocks l's protocol layer
// enforces a copy of. Configuration ValidateServeHTTPListener accepted
// resolves without error.
func ResolveServeHTTPListener(r *Root, l ServeHTTPListener) (ServeHTTPSettings, error) {
	p := httpPlane{root: r, l: l}
	limit, err := p.maxBodyBytes()
	if err != nil {
		return ServeHTTPSettings{}, err
	}
	g := p.guardConfig()
	return ServeHTTPSettings{
		MaxBodyBytes:     limit,
		HostCheck:        g.hostCheck,
		AllowHosts:       g.allowHosts,
		Compression:      p.compressionEnabled(),
		CompressMinBytes: p.compressionMinBytes(),
	}, nil
}

// ServeHTTPHandler wraps inner, the listener's router, in the
// HTTP-plane chain kit owns, outermost first: request id, access log,
// recovery, tracing and metrics (slots 1-5), security headers (6),
// the health probes (7), the Host and Origin checks with the metrics
// endpoint at their inner end (8), the body limit (10) and compression
// (11). Slots 8-11 wrap inner as a whole, whatever path a request
// addresses. Authentication (12) is the listener's own: put it in
// inner, in front of its router, so it runs after the body limit.
func ServeHTTPHandler(r *Root, l ServeHTTPListener, inner http.Handler) (http.Handler, error) {
	p := httpPlane{root: r, l: l}
	return p.wrap(api.Chain(p.guards()...)(inner), l.Routes)
}

// httpPlane is the HTTP-plane chain of one listener: the listener,
// and the root whose configuration and providers it reads.
type httpPlane struct {
	root *Root
	l    ServeHTTPListener
}

// viper is the root's configuration, or nil without a root.
func (p httpPlane) viper() *viper.Viper {
	if p.root == nil {
		return nil
	}
	return p.root.Viper
}

// setting returns the full key that sets key of block for the
// listener's service — its own block first, then services.all — or ""
// when neither does.
func (p httpPlane) setting(block, key string) string {
	_, k, _ := svcconfig.New(p.viper()).Lookup(p.l.Service, block, key)
	return k
}

// validate checks every HTTP-plane block the chain reads, in the
// order the api service always has.
func (p httpPlane) validate() error {
	if err := p.validateHealth(); err != nil {
		return err
	}
	if err := p.validateMetricsScrape(); err != nil {
		return err
	}
	if err := p.validateGuards(); err != nil { // host_check, origin_check, security_headers
		return err
	}
	if _, err := p.maxBodyBytes(); err != nil {
		return err
	}
	return p.validateCompression()
}

// edge is HTTP-plane slots 1-5: request id, access log, recovery, and
// the linked provider's tracing and metrics, which wrap every later
// refusal, probes included.
func (p httpPlane) edge() []api.Middleware {
	logger := kitlog.New(p.viper())
	return []api.Middleware{
		api.RequestID(),
		api.Logger(logger.Info),
		api.Recovery(func(v any, r *http.Request) {
			logger.Error("panic recovered", "error", v, "path", r.URL.Path)
		}),
		p.root.observeMiddleware(p.l.Service),
	}
}

// guards is HTTP-plane slots 10 and 11, the body limit and
// compression, which wrap the router as a whole. Authentication (12)
// is the listener's to append.
func (p httpPlane) guards() []api.Middleware {
	return append([]api.Middleware{p.bodyLimit()}, p.compressionMiddleware()...)
}

// wrap puts slots 1-8 around routed, the router its guards already
// wrap: the metrics endpoint answers at the inner end of slot 8, Host
// and Origin checked but ahead of the guards; the health probes (7)
// answer ahead of slot 8; security headers (6) and the edge wrap
// everything, the probes included. routes is inspected for an adopter
// route at a probe or scrape path.
func (p httpPlane) wrap(routed, routes http.Handler) (http.Handler, error) {
	scraped, err := p.withMetrics(routed, routes)
	if err != nil {
		return nil, err
	}
	checked, err := p.hostOriginChecks(scraped)
	if err != nil {
		return nil, err
	}
	return api.Chain(p.edge()...)(p.securityHeaders(p.withHealth(checked, routes))), nil
}
