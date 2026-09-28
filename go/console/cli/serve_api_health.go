package cli

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
)

// The health block: services.<svc>.health.*, with shared defaults
// under services.all.health.*. Each key resolves on its own — the
// service's key, then the services.all key, then the default — so
// setting one key for the api keeps the others from services.all.
const (
	healthBlock = "health"
	// healthKeyEnabled (default true, on every bind): serve /healthz
	// and /readyz.
	healthKeyEnabled = "enabled"
	// healthKeyPathPrefix (default ""): mount both routes under a
	// prefix, for a tool that wants probes out of its API's
	// namespace.
	healthKeyPathPrefix = "path_prefix"
	// healthKeyDetail: name the failing checks in a 503 from /readyz.
	// Defaults to true on a loopback bind and false on any other,
	// because the routes answer without authentication.
	healthKeyDetail = "detail"

	// sharedServiceBlock is services.all, where shared middleware
	// defaults live. "all" is a reserved service name, so it can
	// never collide with a service.
	sharedServiceBlock = "all"
)

// healthKeys is every key the health block accepts.
var healthKeys = map[string]bool{
	healthKeyEnabled: true, healthKeyPathPrefix: true, healthKeyDetail: true,
}

// DependsOn is the api service's [serve.Dependent] declaration:
// APIConfig.DependsOn. The supervisor starts those services first, and
// /readyz reports not ready while any of them is not.
func (a *apiService) DependsOn() []string { return a.cfg.DependsOn }

// healthBlockKey is services.<svc>.health[.<key>].
func healthBlockKey(svc, key string) string {
	k := serveKeyPrefix + svc + "." + healthBlock
	if key != "" {
		k += "." + key
	}
	return k
}

// healthSetting returns the full key that sets key for the api
// service — its own block first, then services.all — or "" when
// neither does.
func (a *apiService) healthSetting(key string) string {
	if a.root == nil || a.root.Viper == nil {
		return ""
	}
	for _, svc := range []string{APIServiceName, sharedServiceBlock} {
		if k := healthBlockKey(svc, key); a.root.Viper.IsSet(k) {
			return k
		}
	}
	return ""
}

// healthEnabled resolves health.enabled, default true.
func (a *apiService) healthEnabled() bool {
	if k := a.healthSetting(healthKeyEnabled); k != "" {
		return a.root.Viper.GetBool(k)
	}
	return true
}

// healthPathPrefix resolves health.path_prefix and the key that set
// it.
func (a *apiService) healthPathPrefix() (prefix, key string) {
	if k := a.healthSetting(healthKeyPathPrefix); k != "" {
		return a.root.Viper.GetString(k), k
	}
	return "", ""
}

// healthDetail resolves health.detail, whose default follows the
// listen address.
func (a *apiService) healthDetail() bool {
	if k := a.healthSetting(healthKeyDetail); k != "" {
		return a.root.Viper.GetBool(k)
	}
	return isLoopbackAddr(a.listenAddr())
}

// validateHealth is the health half of the configuration gate. An
// unknown key in either health block is refused, because a misspelled
// key silently leaves the default in force. A prefix that is not an
// absolute, clean URL path would mount probes somewhere no
// orchestrator is pointed at.
func (a *apiService) validateHealth() error {
	if a.root != nil && a.root.Viper != nil {
		for _, svc := range []string{APIServiceName, sharedServiceBlock} {
			block := healthBlockKey(svc, "")
			if !a.root.Viper.IsSet(block) {
				continue
			}
			var unknown []string
			for k := range a.root.Viper.GetStringMap(block) {
				if !healthKeys[k] {
					unknown = append(unknown, block+"."+k)
				}
			}
			if len(unknown) > 0 {
				sort.Strings(unknown)
				return fmt.Errorf("%s: unknown key; %s accepts %s, %s and %s",
					strings.Join(unknown, ", "), block,
					healthKeyEnabled, healthKeyPathPrefix, healthKeyDetail)
			}
		}
	}

	p, key := a.healthPathPrefix()
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" ||
		strings.ContainsAny(p, "?# \t") {
		return fmt.Errorf(
			"%s: %q must be an absolute URL path with no trailing slash, like /_kit",
			key, p,
		)
	}
	return nil
}

// withHealth puts the liveness and readiness routes in front of h.
//
// This is HTTP-plane slot 7. The probes are answered before h and end
// the request there, so nothing in the router's chain — Host and
// Origin checks, body limit, authentication, and the invocation plane
// behind it (rate limit, audit) — ever sees one: orchestrator probes
// address a pod by IP and carry no credentials. Slots 1–6 (request
// id, access log, recovery, telemetry, security headers) wrap the
// handler this returns. An adopter route at exactly a probe path
// wins over it.
//
// Readiness is the api service's own Ready AND each DependsOn service
// being ready. For a dependency, ready means the supervisor recorded
// it ready (not starting, failed, or stopped) and its own Ready still
// agrees. A dependency the run did not start is not checked, the same
// way it does not constrain start order.
func (a *apiService) withHealth(ctx context.Context, h http.Handler) http.Handler {
	if !a.healthEnabled() {
		return h
	}
	view, hasView := serve.RunViewFrom(ctx)
	checks := []api.ReadinessCheck{{Name: APIServiceName, Ready: a.Ready}}
	for _, dep := range a.cfg.DependsOn {
		checks = append(checks, api.ReadinessCheck{
			Name:  dep,
			Ready: func() bool { return a.dependencyReady(dep, view, hasView) },
		})
	}
	prefix, _ := a.healthPathPrefix()
	return api.HealthRoutes(h, api.HealthConfig{
		PathPrefix: prefix,
		Checks:     checks,
		Detail:     a.healthDetail(),
	})
}

// dependencyReady reports whether dep is ready as far as this run is
// concerned.
func (a *apiService) dependencyReady(dep string, view serve.RunView, hasView bool) bool {
	if hasView {
		switch view.State(dep) {
		case serve.StateNotInRun:
			return true
		case serve.StateReady:
		default:
			return false
		}
	}
	if a.root == nil || a.root.serveReg == nil {
		return true
	}
	svc, ok := a.root.serveReg.Lookup(dep)
	if !ok {
		return true
	}
	return svc.Ready()
}
