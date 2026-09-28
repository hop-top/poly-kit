package cli

import (
	"hop.top/kit/go/transport/cmdsurface"
)

// ReasonWithheldByConfig is the discovery reason for a command the
// adopter kept off REST through APIConfig.Hide or APIConfig.Expose.
// It is [cmdsurface.ReasonWithheldByConfig]: the projection the api
// service serves is the bridge's.
const ReasonWithheldByConfig = cmdsurface.ReasonWithheldByConfig

// projectionBridge builds the bridge the api service's projection
// executes through, exposed on REST as cfg says.
//
// It is built HERE, at service start, rather than at registration:
// WithAPI runs while the tree is still being assembled, and a tree
// reflected mid-construction describes commands the binary does not
// expose. By the time a service starts, cobra has the whole tree.
//
// exp is the service's exposure; it picks the rate limit's default
// and whether kit-default applies. extra are further bridge options the service resolved
// itself (the result cache); they go before the shared options.
func projectionBridge(r *Root, cfg *APIConfig, exp ServeExposure, extra ...cmdsurface.Option) (*cmdsurface.Bridge, error) {
	// The permission gate and the audit sinks are resolved now, at
	// start: --policy is parsed by then and every adopter option has
	// run.
	shared, err := r.serveBridgeOptions(APIServiceName, exp)
	if err != nil {
		return nil, err
	}
	limit, err := r.serveRateLimitOptions(APIServiceName, exp.Loopback)
	if err != nil {
		return nil, err
	}
	// A zero Policy is behaviorally identical to DefaultPolicy() on
	// every surface, so passing the adopter's value through
	// unconditionally preserves today's behavior when they set
	// nothing.
	//
	// The runner comes next: the per-invocation runner when the
	// adopter supplied a root factory, otherwise nothing, and the
	// bridge builds its shared-tree runner over root. The shared
	// options go last so a test-injected Runner still wins.
	opts := append([]cmdsurface.Option{cmdsurface.WithPolicy(cfg.Policy)}, r.serveRunnerOptions()...)
	opts = append(opts, r.serveObservabilityOptions(APIServiceName)...)
	opts = append(opts, limit...)
	opts = append(opts, extra...)
	opts = append(opts, shared...)
	bridge := cmdsurface.New(r.Cmd, opts...)
	// Exposing REST here is what "no adopter mounting code" means:
	// the bridge's default enabled set is CLI + Lib + MCP, so a leaf
	// would otherwise refuse every projected call with
	// ErrSurfaceNotEnabled. The adopter used to write this Expose;
	// registering the api service now implies it.
	//
	// This widens enablement, not authorization. The destructive
	// ceiling is Policy.Allowed, which Expose does not touch: a
	// destructive leaf stays refused on REST unless the adopter's
	// policy names the surface.
	// An empty Expose reaches the whole tree, which is what makes
	// projection automatic; a non-empty one narrows it.
	if len(cfg.Expose) == 0 {
		bridge.Expose("*", cmdsurface.SurfaceREST)
	}
	for _, pattern := range cfg.Expose {
		bridge.Expose(pattern, cmdsurface.SurfaceREST)
	}
	// Hide runs after Expose so it carves exceptions out of it.
	for _, pattern := range cfg.Hide {
		bridge.Hide(pattern, cmdsurface.SurfaceREST)
	}
	return bridge, nil
}
