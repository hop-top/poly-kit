package admitted

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
	// SurfaceREST is request/reply over HTTP: the command projection
	// under /v1/commands (MountProjection), and the deprecated
	// MountREST (POST /cmd/{path...} with an Invocation body).
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
	// SurfaceSocket is newline-delimited JSON over a local Unix domain
	// socket (see hop.top/kit/go/transport/socket). It is its own
	// surface, not an alias of SurfaceRPC: a destructive grant on one
	// never reaches the other, and audit records name the socket.
	SurfaceSocket Surface = "socket"
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
		SurfaceSocket,
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
