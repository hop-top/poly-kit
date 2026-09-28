package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/viper"

	"hop.top/kit/go/transport/api"
)

// Middleware blocks the api service's transport guards read, each
// under services.api.<block> and, as a shared default, under
// services.all.<block>.
const (
	blockHostCheck       = "host_check"
	blockOriginCheck     = "origin_check"
	blockSecurityHeaders = "security_headers"
)

// guardBlockKeys lists the keys each guard block accepts; any other
// key inside one of these blocks is a configuration error.
var guardBlockKeys = map[string][]string{
	blockHostCheck:       {"enabled", "allow"},
	blockOriginCheck:     {"enabled", "allow"},
	blockSecurityHeaders: {"enabled"},
}

// middlewareKey resolves one middleware key to the config key that
// supplies it: services.<svc>.<block>.<key> from any source, then
// services.all.<block>.<key>. ok is false when neither is set and
// the code default applies.
//
// It is the only place guard keys are looked up, so a shared
// resolver can replace it without touching the guards.
func middlewareKey(v *viper.Viper, svc, block, key string) (string, bool) {
	if v == nil {
		return "", false
	}
	for _, scope := range []string{svc, serveAllScope} {
		k := serveKeyPrefix + scope + "." + block + "." + key
		if v.IsSet(k) {
			return k, true
		}
	}
	return "", false
}

// guardConfig is the resolved transport-guard configuration.
type guardConfig struct {
	hostCheck    bool
	allowHosts   []string // configured, before the listener's own hosts
	originCheck  bool
	allowOrigins []string
	headers      bool
}

func (a *apiService) guardConfig() guardConfig {
	g := guardConfig{hostCheck: true, originCheck: true, headers: true}
	if a.root == nil || a.root.Viper == nil {
		return g
	}
	v := a.root.Viper
	boolKey := func(block string, dst *bool) {
		if k, ok := middlewareKey(v, APIServiceName, block, "enabled"); ok {
			*dst = v.GetBool(k)
		}
	}
	boolKey(blockHostCheck, &g.hostCheck)
	boolKey(blockOriginCheck, &g.originCheck)
	boolKey(blockSecurityHeaders, &g.headers)
	if k, ok := middlewareKey(v, APIServiceName, blockHostCheck, "allow"); ok {
		g.allowHosts = v.GetStringSlice(k)
	}
	if k, ok := middlewareKey(v, APIServiceName, blockOriginCheck, "allow"); ok {
		g.allowOrigins = v.GetStringSlice(k)
	}
	return g
}

// validateGuards is the configuration gate for the guard blocks: an
// unknown key inside one, or an allowed origin that is not a bare
// origin, fails validation rather than the start.
func (a *apiService) validateGuards() error {
	if a.root != nil && a.root.Viper != nil {
		if err := checkGuardBlockKeys(a.root.Viper, APIServiceName); err != nil {
			return err
		}
	}
	_, err := a.hostOriginChecks(http.NotFoundHandler())
	return err
}

func checkGuardBlockKeys(v *viper.Viper, svc string) error {
	for _, k := range v.AllKeys() {
		for _, scope := range []string{svc, serveAllScope} {
			for block, known := range guardBlockKeys {
				prefix := serveKeyPrefix + scope + "." + block
				if k == prefix {
					return fmt.Errorf("%s: must be a block with keys %s", k, strings.Join(known, ", "))
				}
				rest, found := strings.CutPrefix(k, prefix+".")
				if !found {
					continue
				}
				if !containsString(known, rest) {
					return fmt.Errorf("%s: unknown key %q; %s accepts %s",
						k, rest, block, strings.Join(known, ", "))
				}
			}
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// hostOriginChecks wraps h in the Host and Origin checks, HTTP-plane
// slot 8: inside the health endpoints (slot 7), so an orchestrator
// probe addressing a pod by IP is answered before them, and outside
// CORS, limits and authentication, so a rebinding or cross-origin
// request is refused before anything else reads it. Host runs first,
// so "same origin" means a host this server answers for.
//
// The allowed hosts are the listener's own — [api.ListenerHosts] of
// the bound address — plus host_check.allow. A wildcard bind knows
// no names of its own, so with no allow list it gets no Host check.
func (a *apiService) hostOriginChecks(h http.Handler) (http.Handler, error) {
	g := a.guardConfig()
	var mws []api.Middleware
	if g.hostCheck {
		hosts, wildcard := api.ListenerHosts(a.listenAddr())
		hosts = append(hosts, g.allowHosts...)
		if !wildcard || len(g.allowHosts) > 0 {
			mws = append(mws, api.HostCheck(api.HostCheckConfig{Allow: hosts}))
		}
	}
	if g.originCheck {
		origin, err := api.OriginCheck(api.OriginCheckConfig{Allow: g.allowOrigins})
		if err != nil {
			return nil, fmt.Errorf("%s%s.%s.allow: %w", serveKeyPrefix, APIServiceName, blockOriginCheck, err)
		}
		mws = append(mws, origin)
	}
	return api.Chain(mws...)(h), nil
}

// securityHeaders wraps h in the security headers, HTTP-plane slot 6:
// outside the health endpoints and the Host and Origin checks, so no
// response — a refusal included — leaves without them.
func (a *apiService) securityHeaders(h http.Handler) http.Handler {
	if !a.guardConfig().headers {
		return h
	}
	return api.SecurityHeaders(api.SecurityHeadersConfig{})(h)
}
