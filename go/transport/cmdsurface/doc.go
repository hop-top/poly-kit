// Package cmdsurface bridges a single cobra command tree to many
// invocation surfaces (CLI, REST, WebSocket, SSE, ConnectRPC, MCP,
// webhook, bus, cron, library, OAuth callback, signed URL, FaaS,
// Unix socket).
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
// land in subsequent waves as separate files. The full surface
// inventory is the Surface constants declared below; their canonical
// declarations, with each one's meaning, are in the leaf package
// [hop.top/kit/go/transport/cmdsurface/admitted].
package cmdsurface

import "hop.top/kit/go/transport/cmdsurface/admitted"

// Surface identifies an invocation transport. It is the leaf type
// [admitted.Surface], declared there with its constants, AllSurfaces,
// IsValid and String, so a command can name a surface without linking
// the bridge; the names below are the same values.
type Surface = admitted.Surface

// Canonical surface identifiers; each one is documented on its
// [admitted] declaration.
const (
	SurfaceCLI     = admitted.SurfaceCLI
	SurfaceREST    = admitted.SurfaceREST
	SurfaceWS      = admitted.SurfaceWS
	SurfaceSSE     = admitted.SurfaceSSE
	SurfaceRPC     = admitted.SurfaceRPC
	SurfaceMCP     = admitted.SurfaceMCP
	SurfaceWebhook = admitted.SurfaceWebhook
	SurfaceBus     = admitted.SurfaceBus
	SurfaceCron    = admitted.SurfaceCron
	SurfaceLib     = admitted.SurfaceLib
	SurfaceOAuthCB = admitted.SurfaceOAuthCB
	SurfaceSigned  = admitted.SurfaceSigned
	SurfaceFaaS    = admitted.SurfaceFaaS
	SurfaceSocket  = admitted.SurfaceSocket
)

// AllSurfaces is the canonical list of every defined Surface, in
// declaration order ([admitted.AllSurfaces]).
func AllSurfaces() []Surface { return admitted.AllSurfaces() }
