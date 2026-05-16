// Package cmdsurface bridges a single cobra command tree to many
// invocation surfaces (CLI, REST, WebSocket, SSE, ConnectRPC, MCP,
// webhook, bus, cron, library, OAuth callback, signed URL, FaaS).
//
// Kit already gives every adopter a unified command tree via the
// cobra root produced by [hop.top/kit/go/console/cli] and projects
// it onto the CLI surface (the adopter's binary) and the MCP surface
// (via [hop.top/kit/go/ai/toolspec/adapters]). The cmdsurface package
// is the substrate that lets the SAME cobra leaves be reached by
// additional transports — REST and ConnectRPC handlers (see
// [hop.top/kit/go/transport/api] and [hop.top/kit/go/transport/rpc]),
// streaming subscribers, scheduled jobs, FaaS adapters — without
// adopters writing per-surface handlers.
//
// # Relationship to existing kit packages
//
//   - [hop.top/kit/go/console/cli] is the SOURCE: the cobra tree the
//     bridge projects from. cmdsurface does not mutate the source
//     tree — it reads annotations and resolves leaves on demand.
//   - [hop.top/kit/go/transport/api] and
//     [hop.top/kit/go/transport/rpc] are PEER transports. The bridge
//     handles command invocations; the existing Service[T] /
//     ResourceRouter[T] machinery handles CRUD entity resources.
//     The two abstractions coexist; an adopter picks per route.
//   - [hop.top/kit/go/ai/toolspec] holds the discovery model. The
//     bridge reads the same cobra annotations (kit/side-effect,
//     kit/auth-required, kit/exit-codes, kit/args, kit/idempotent)
//     and reuses toolspec.Safety semantics where helpful.
//
// # Foundation wave scope
//
// This package, in its foundation wave, declares:
//
//   - The invocation model (Invocation, Result, Event, Meta).
//   - The Runner interface and InProcessRunner reference impl.
//   - The Safety classification + Policy gate.
//   - The Bridge struct with Expose/Hide/Invoke/Leaves.
//   - YAML config loading.
//
// Surface implementations (REST handler, ConnectRPC service, etc.)
// land in subsequent waves as separate files. See the track spec at
// .tlc/tracks/cmdsurf/spec.md for the full surface inventory and
// wave breakdown.
package cmdsurface

// Surface identifies an invocation transport. Surfaces are
// orthogonal to the cobra leaf they expose — the same leaf can be
// reachable on any subset of surfaces, gated by policy.
type Surface string

// Canonical surface identifiers. The string values are the YAML keys
// adopters write in config under surfaces.commands.<path>.enabled
// and surfaces.defaults.
const (
	// SurfaceCLI is the local cobra surface (the adopter's binary).
	// Always available; the bridge never disables this surface — it
	// is the source the other surfaces project from.
	SurfaceCLI Surface = "cli"
	// SurfaceREST is request/reply over HTTP. POST /cmd/{path...}
	// with an Invocation body returns a Result.
	SurfaceREST Surface = "rest"
	// SurfaceWS is bidirectional WebSocket. Frames carry Invocations
	// in and Events out.
	SurfaceWS Surface = "ws"
	// SurfaceSSE is one-way server-sent events.
	// GET /cmd/{path...}/stream streams Events.
	SurfaceSSE Surface = "sse"
	// SurfaceRPC is ConnectRPC. Invoke + InvokeStream methods.
	SurfaceRPC Surface = "rpc"
	// SurfaceMCP is the Model Context Protocol tools/call channel.
	SurfaceMCP Surface = "mcp"
	// SurfaceWebhook is an inbound HTTP hook: POST /hooks/{name}
	// maps a template payload to an Invocation.
	SurfaceWebhook Surface = "webhook"
	// SurfaceBus is pub/sub: subscribe cmd.{path}.req, publish .resp.
	SurfaceBus Surface = "bus"
	// SurfaceCron is a scheduled trigger.
	SurfaceCron Surface = "cron"
	// SurfaceLib is the in-process Go API: b.Invoke(ctx, inv).
	SurfaceLib Surface = "lib"
	// SurfaceOAuthCB is an inbound OAuth callback handler:
	// GET /oauth/{provider}/callback maps to an Invocation.
	SurfaceOAuthCB Surface = "oauth-cb"
	// SurfaceSigned is a one-shot signed-URL exec link.
	SurfaceSigned Surface = "signed"
	// SurfaceFaaS is a FaaS adapter (AWS Lambda, Cloud Run) wrapping
	// the same Runner under provider invocation contracts.
	SurfaceFaaS Surface = "faas"
)

// AllSurfaces is the canonical list of every defined Surface,
// returned in declaration order. Callers iterating surfaces (config
// validators, capability emitters) prefer this over an ad-hoc slice
// so a new surface added here is automatically picked up.
func AllSurfaces() []Surface {
	return []Surface{
		SurfaceCLI, SurfaceREST, SurfaceWS, SurfaceSSE, SurfaceRPC,
		SurfaceMCP, SurfaceWebhook, SurfaceBus, SurfaceCron,
		SurfaceLib, SurfaceOAuthCB, SurfaceSigned, SurfaceFaaS,
	}
}

// IsValid reports whether s is one of the declared surfaces.
func (s Surface) IsValid() bool {
	for _, x := range AllSurfaces() {
		if x == s {
			return true
		}
	}
	return false
}

// String returns the surface identifier as written in config.
func (s Surface) String() string { return string(s) }
