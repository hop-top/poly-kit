package llm

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"hop.top/kit/go/core/netpolicy"
)

// Locality says how far an endpoint is from this machine.
type Locality string

const (
	// LocalityLoopback is this host: 127.0.0.0/8, ::1, localhost.
	LocalityLoopback Locality = "loopback"
	// LocalityPrivate is the local network: RFC 1918, fc00::/7, *.local.
	LocalityPrivate Locality = "private"
	// LocalityRemote is anything else: a public or CGNAT/tailnet
	// address, link-local, an unspecified address, or a DNS name kit
	// cannot tell is local.
	LocalityRemote Locality = "remote"
)

// ErrNoEndpoint is returned by [Endpoint] when the URI names no
// endpoint and the scheme declares none: the adapter picks its own
// default, which kit cannot read without building it.
var ErrNoEndpoint = errors.New("llm: scheme declares no endpoint")

// Endpoint returns the base URL [Resolve] would send uri's requests
// to, deciding as Resolve does, highest first: the URI's base_url
// param, its host:port, the scheme's declared default
// ([Declaration.BaseURL]), the aim catalog's base URL. Failing all of
// those, the first of the scheme's declared [Declaration.Endpoints],
// where an adapter states the default it picks itself. It builds no
// adapter and reads no network. [Hermetic] and [WithTransport] are
// accepted and [Hermetic] narrows the lookup as it does for Resolve.
//
// It returns [ErrNoEndpoint] when none of those names one.
//
// An adapter may still pick another endpoint from its own environment
// variables (anthropic's ANTHROPIC_BASE_URL) when none is configured;
// [Hermetic] closes that, and [LocalOnly] checks each request's host
// whatever Endpoint said.
func Endpoint(uri string, opts ...ResolveOption) (string, error) {
	return DefaultRegistry.Endpoint(uri, opts...)
}

// Endpoint is [Endpoint] against r.
func (r *Registry) Endpoint(uri string, opts ...ResolveOption) (string, error) {
	var o resolveOptions
	for _, opt := range opts {
		opt(&o)
	}
	rt, cfg, err := r.resolveConfig(uri, o)
	if err != nil {
		return "", err
	}
	return endpointOf(rt, cfg)
}

func endpointOf(rt route, cfg ResolvedConfig) (string, error) {
	if cfg.Provider.BaseURL != "" {
		return cfg.Provider.BaseURL, nil
	}
	if len(rt.decl.Endpoints) > 0 {
		return rt.decl.Endpoints[0], nil
	}
	return "", fmt.Errorf("%w: %q", ErrNoEndpoint, cfg.URI.Scheme)
}

// EndpointLocality classifies baseURL by its host alone, with no DNS
// lookup: loopback for 127.0.0.0/8, ::1 and localhost; private for
// RFC 1918, fc00::/7 and *.local; remote for every other address
// (CGNAT and tailnet 100.64.0.0/10 included) and every other name,
// which is never resolved. An error when baseURL is not an http(s) URL
// kit would send a request to: it must parse, carry a host and no
// userinfo, backslash, percent-escape, whitespace or control character.
func EndpointLocality(baseURL string) (Locality, error) {
	e, ok := parseEndpoint(baseURL)
	if !ok || (e.scheme != "http" && e.scheme != "https") {
		return "", fmt.Errorf("llm: endpoint %q is not a plain http(s) URL", RedactURI(baseURL))
	}
	return hostLocality(e.host), nil
}

// hostLocality classifies a bare host (no port), lowercased or not.
func hostLocality(host string) Locality {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if netpolicy.IsLoopback(h) {
		return LocalityLoopback
	}
	if ip := net.ParseIP(h); ip != nil {
		return ipLocality(ip)
	}
	if strings.HasSuffix(h, ".local") {
		return LocalityPrivate
	}
	return LocalityRemote
}

func ipLocality(ip net.IP) Locality {
	switch {
	case ip.IsLoopback():
		return LocalityLoopback
	case ip.IsPrivate():
		return LocalityPrivate
	}
	return LocalityRemote
}
