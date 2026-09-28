package cli

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
)

// The health block: services.<svc>.health.*, with shared defaults
// under services.all.health.*. Each key resolves on its own — the
// service's key, then the services.all key, then the default — so
// setting one key for the api keeps the others from services.all.
// The keys are registered in svcconfig.
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
)

// DependsOn is the api service's [serve.Dependent] declaration:
// APIConfig.DependsOn. The supervisor starts those services first, and
// /readyz reports not ready while any of them is not.
func (a *apiService) DependsOn() []string { return a.cfg.DependsOn }

// healthSetting returns the full key that sets key for the
// listener's service — its own block first, then services.all — or ""
// when neither does.
func (p httpPlane) healthSetting(key string) string {
	return p.setting(healthBlock, key)
}

// healthEnabled resolves health.enabled, default true.
func (p httpPlane) healthEnabled() bool {
	if k := p.healthSetting(healthKeyEnabled); k != "" {
		return p.root.Viper.GetBool(k)
	}
	return true
}

// healthPathPrefix resolves health.path_prefix and the key that set
// it.
func (p httpPlane) healthPathPrefix() (prefix, key string) {
	if k := p.healthSetting(healthKeyPathPrefix); k != "" {
		return p.root.Viper.GetString(k), k
	}
	return "", ""
}

// healthDetail resolves health.detail, whose default follows the
// listen address.
func (p httpPlane) healthDetail() bool {
	if k := p.healthSetting(healthKeyDetail); k != "" {
		return p.root.Viper.GetBool(k)
	}
	return isLoopbackAddr(p.l.Addr)
}

// validateHealth is the health half of the configuration gate. An
// unknown key in either health block is refused, because a misspelled
// key silently leaves the default in force. A prefix that is not an
// absolute, clean URL path would mount probes somewhere no
// orchestrator is pointed at.
func (p httpPlane) validateHealth() error {
	if v := p.viper(); v != nil {
		err := svcconfig.New(v).ValidateBlock(healthBlock, p.l.Service, svcconfig.Shared)
		if err != nil {
			return err
		}
	}

	prefix, key := p.healthPathPrefix()
	if prefix == "" {
		return nil
	}
	if !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix || prefix == "/" ||
		strings.ContainsAny(prefix, "?# \t") {
		return fmt.Errorf(
			"%s: %q must be an absolute URL path with no trailing slash, like /_kit",
			key, prefix,
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
// Readiness is the service's own Ready AND every further check the
// listener names.
func (p httpPlane) withHealth(h, routes http.Handler) http.Handler {
	if !p.healthEnabled() {
		return h
	}
	ready := p.l.Ready
	if ready == nil {
		ready = func() bool { return true }
	}
	checks := append([]api.ReadinessCheck{{Name: p.l.Service, Ready: ready}}, p.l.Checks...)
	prefix, _ := p.healthPathPrefix()
	return api.HealthRoutes(h, api.HealthConfig{
		PathPrefix: prefix,
		Checks:     checks,
		Detail:     p.healthDetail(),
		Routes:     routes,
	})
}

// dependencyChecks are the api service's readiness inputs beyond its
// own Ready: each DependsOn service being ready. For a dependency,
// ready means the supervisor recorded it ready (not starting, failed,
// or stopped) and its own Ready still agrees. A dependency the run
// did not start is not checked, the same way it does not constrain
// start order.
func (a *apiService) dependencyChecks(ctx context.Context) []api.ReadinessCheck {
	view, hasView := serve.RunViewFrom(ctx)
	var checks []api.ReadinessCheck
	for _, dep := range a.cfg.DependsOn {
		checks = append(checks, api.ReadinessCheck{
			Name:  dep,
			Ready: func() bool { return a.dependencyReady(dep, view, hasView) },
		})
	}
	return checks
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
