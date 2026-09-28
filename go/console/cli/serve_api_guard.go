package cli

import (
	"fmt"
	"net/http"
	"slices"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// Middleware blocks the transport guards of a kit HTTP listener read,
// each under services.<svc>.<block> and, as a shared default, under
// services.all.<block>. Their keys are registered in svcconfig.
const (
	blockHostCheck       = "host_check"
	blockOriginCheck     = "origin_check"
	blockSecurityHeaders = "security_headers"
)

// guardConfig is the resolved transport-guard configuration.
type guardConfig struct {
	hostCheck    bool
	allowHosts   []string // configured, before the listener's own hosts
	originCheck  bool
	allowOrigins []string
	headers      bool
}

func (p httpPlane) guardConfig() guardConfig {
	g := guardConfig{hostCheck: true, originCheck: true, headers: true}
	v := p.viper()
	if v == nil {
		return g
	}
	boolKey := func(block string, dst *bool) {
		if k := p.setting(block, "enabled"); k != "" {
			*dst = v.GetBool(k)
		}
	}
	boolKey(blockHostCheck, &g.hostCheck)
	boolKey(blockOriginCheck, &g.originCheck)
	boolKey(blockSecurityHeaders, &g.headers)
	if k := p.setting(blockHostCheck, "allow"); k != "" {
		g.allowHosts = v.GetStringSlice(k)
	}
	if k := p.setting(blockOriginCheck, "allow"); k != "" {
		g.allowOrigins = v.GetStringSlice(k)
	}
	return g
}

// validateGuards is the configuration gate for the api service's
// guard blocks.
func (a *apiService) validateGuards() error { return a.plane().validateGuards() }

// validateGuards is the configuration gate for the guard blocks: an
// unknown key inside one, or an allowed origin that is not a bare
// origin, fails validation rather than the start.
func (p httpPlane) validateGuards() error {
	if v := p.viper(); v != nil {
		cfg := svcconfig.New(v)
		for _, block := range []string{blockHostCheck, blockOriginCheck, blockSecurityHeaders} {
			if err := cfg.ValidateBlock(block, p.l.Service, svcconfig.Shared); err != nil {
				return err
			}
		}
	}
	_, err := p.hostOriginChecks(http.NotFoundHandler())
	return err
}

// hostOriginChecks wraps h in the api service's Host and Origin checks.
func (a *apiService) hostOriginChecks(h http.Handler) (http.Handler, error) {
	return a.plane().hostOriginChecks(h)
}

// hostOriginChecks wraps h in the Host and Origin checks, HTTP-plane
// slot 8: inside the health endpoints (slot 7), so an orchestrator
// probe addressing a pod by IP is answered before them, and outside
// the metrics endpoint, CORS, limits and authentication, so a
// rebinding or cross-origin request is refused before anything else
// reads it. Host runs first, so "same origin" means a host this server
// answers for. The Origin check admits, beside origin_check.allow, the
// origins the cors block grants by name.
//
// The allowed hosts are the listener's own — [api.ListenerHosts] of
// the configured address — plus host_check.allow. A wildcard bind
// knows no names of its own, so with no allow list it gets no Host
// check. Refusals are written in the listener's protocol.
func (p httpPlane) hostOriginChecks(h http.Handler) (http.Handler, error) {
	g := p.guardConfig()
	var mws []api.Middleware
	if g.hostCheck {
		hosts, wildcard := api.ListenerHosts(p.l.Addr)
		hosts = append(hosts, g.allowHosts...)
		if !wildcard || len(g.allowHosts) > 0 {
			mws = append(mws, api.HostCheck(api.HostCheckConfig{Allow: hosts, Refuse: p.l.Refuse}))
		}
	}
	if g.originCheck {
		// An origin the cors block grants by name may write too: a
		// grant to read a service whose calls are POSTs would grant
		// nothing. Every other origin stays refused.
		allow := append(slices.Clone(g.allowOrigins), p.corsOrigins()...)
		origin, err := api.OriginCheck(api.OriginCheckConfig{Allow: allow, Refuse: p.l.Refuse})
		if err != nil {
			return nil, fmt.Errorf("%s%s.%s.allow: %w", serveKeyPrefix, p.l.Service, blockOriginCheck, err)
		}
		mws = append(mws, origin)
	}
	return api.Chain(mws...)(h), nil
}

// securityHeaders wraps h in the api service's security headers.
func (a *apiService) securityHeaders(h http.Handler) http.Handler {
	return a.plane().securityHeaders(h)
}

// securityHeaders wraps h in the security headers, HTTP-plane slot 6:
// outside the health endpoints and the Host and Origin checks, so no
// response — a refusal included — leaves without them.
func (p httpPlane) securityHeaders(h http.Handler) http.Handler {
	if !p.guardConfig().headers {
		return h
	}
	return api.SecurityHeaders(api.SecurityHeadersConfig{})(h)
}
