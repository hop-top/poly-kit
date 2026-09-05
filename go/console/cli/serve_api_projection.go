package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// ReasonWithheldByConfig is the discovery reason for a command the
// adopter kept off REST through APIConfig.WithholdCommands.
//
// The projection owns this reason rather than the reflector: the
// reflector's vocabulary answers "is this command projectable at
// all", and the answer here is yes — this deployment chose not to.
// The spelling follows the reflector's hyphenated-lowercase
// convention so a client switching on the field sees one vocabulary.
const ReasonWithheldByConfig = "withheld-by-config"

// buildProjection reflects root and returns the projection config the
// api service mounts, together with the bridge that executes it.
//
// Reflection happens HERE, at service start, rather than at
// registration: WithAPI runs while the tree is still being assembled,
// and a tree reflected mid-construction describes commands the binary
// does not expose. By the time a service starts, cobra has the whole
// tree.
//
// The reflector is told to allow interactive and reserved commands so
// they are DESCRIBED, then the projection withholds them. Dropping
// them at reflection time would make them vanish from discovery, and
// "this command exists but you cannot call it here" is exactly the
// answer the track exists to give.
func buildProjection(
	root *cobra.Command, name, version string, r *Root, cfg *APIConfig,
) (api.ProjectionConfig, *cmdsurface.Bridge, error) {
	// No Allow* options. Each one makes a class of command
	// INVOCABLE, not merely described — the reflector describes
	// every command unconditionally. Passing AllowInteractive here
	// would mount a shell over HTTP, which is exactly what the
	// track puts out of scope; the descriptors still appear in
	// discovery carrying their reason, which is what "reflect
	// everything" asks for.
	tree := cmdreflect.Reflect(root, cmdreflect.WithReserved(r))

	// The permission gate and the audit sinks are resolved now, at
	// start: --policy is parsed by then and every adopter option has
	// run.
	shared, err := r.serveBridgeOptions()
	if err != nil {
		return api.ProjectionConfig{}, nil, err
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
	opts = append(opts, shared...)
	bridge := cmdsurface.New(root, opts...)
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

	pcfg := api.ProjectionConfig{
		ToolName:    name,
		ToolVersion: version,
		Executor:    &bridgeExecutor{bridge: bridge},
	}

	leaves := leafByKey(bridge)
	for _, d := range tree.Descriptors {
		// The root itself and pure command groups are not calls.
		if d.IsRoot() || d.Surface.HasSubCommands {
			continue
		}
		pcfg.Descriptors = append(pcfg.Descriptors,
			descriptorToProjection(d, bridge, leaves[d.PathKey()]))
	}
	return pcfg, bridge, nil
}

// descriptorToProjection converts one reflected descriptor into the
// transport-neutral shape the api package projects.
//
// Invocability is the reflector's verdict AND the bridge's: a command
// the reflector allows but policy refuses on the REST surface must be
// withheld with a reason, not mounted and then refused per-call. The
// bridge is the authority on policy, so it is consulted here rather
// than a second rule being written. leaf is the bridge's view of the
// same command, nil when the bridge did not discover it.
func descriptorToProjection(
	d *cmdreflect.Descriptor, b *cmdsurface.Bridge, leaf *cmdsurface.Leaf,
) api.CommandDescriptor {
	out := api.CommandDescriptor{
		Path:                 append([]string(nil), d.Path[1:]...),
		Summary:              d.Short,
		Description:          d.Long,
		SideEffect:           sideEffectClass(d.Safety.Tier),
		Invocable:            d.Invocable,
		Reason:               string(d.Reason),
		RequiresConfirmation: d.Safety.RequiresConfirmation,
		RequiresConfirmToken: d.Safety.DestructiveTokenRequired,
		AuthRequired:         d.Safety.AuthRequired,
		OutputSchema:         d.Output.Schema,
	}

	for _, f := range d.Flags {
		// Hidden and deprecated flags are part of the command but
		// not part of its supported surface; publishing them would
		// invite a caller to depend on what the adopter is retiring.
		if f.Hidden || f.Deprecated {
			continue
		}
		out.Flags = append(out.Flags, api.CommandFlag{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Default:     f.Default,
			Required:    f.Required,
		})
	}
	for _, a := range d.Args {
		out.Args = append(out.Args, api.CommandArg{Name: a.Name, Required: a.Required})
	}

	if out.Invocable {
		switch {
		case !restEnabled(leaf):
			// The adopter's withhold list took this one off REST.
			// A distinct reason keeps "we chose not to" separable
			// from "policy forbids it" in an operator's listing.
			out.Invocable = false
			out.Reason = ReasonWithheldByConfig
		case !policyAllowsREST(d, b):
			// Reuse the reflector's own vocabulary rather than
			// minting a REST-specific token: the caller's question
			// is the same one, and a second spelling would
			// fragment the enum.
			out.Invocable = false
			out.Reason = string(cmdreflect.ReasonUnauthorizedDestructive)
		case deniedForEveryone(b, leaf):
			// The permission gate refuses this command whoever
			// asks, so there is no caller for whom a route would
			// answer. A caller-specific refusal is not visible
			// here: the command stays mounted and the gate answers
			// per call.
			out.Invocable = false
			out.Reason = cmdsurface.ReasonPermissionDenied
		}
	}
	return out
}

// restEnabled reports whether the leaf is still exposed on REST after
// the withhold patterns were applied.
//
// A descriptor with no leaf on the bridge is treated as enabled: the
// reflector already judged it invocable, and the absence means the
// bridge never discovered it, which policyAllowsREST answers for.
func restEnabled(leaf *cmdsurface.Leaf) bool {
	if leaf == nil {
		return true
	}
	return leaf.Enabled[cmdsurface.SurfaceREST]
}

// policyAllowsREST asks the bridge whether the leaf may be invoked
// over REST at all.
func policyAllowsREST(d *cmdreflect.Descriptor, b *cmdsurface.Bridge) bool {
	if b == nil {
		return true
	}
	return b.Policy().Allowed(cmdsurface.SafetyClass{
		Destructive:  d.Safety.Destructive(),
		AuthRequired: d.Safety.AuthRequired,
	}, cmdsurface.SurfaceREST)
}

// deniedForEveryone asks the permission gate with a Meta that names
// only the surface, and honors a refusal only when the gate says the
// verdict does not depend on the caller. Discovery cannot know who
// will call; it can only withhold what nobody may call.
func deniedForEveryone(b *cmdsurface.Bridge, leaf *cmdsurface.Leaf) bool {
	if b == nil || leaf == nil {
		return false
	}
	dec := b.Permission(context.Background(),
		cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}, leaf)
	return !dec.Allowed && dec.CallerIndependent
}

// sideEffectClass projects the six-tier ladder onto the three
// distinctions that change an HTTP decision.
func sideEffectClass(t cmdreflect.Tier) api.SideEffectClass {
	switch t {
	case cmdreflect.TierRead:
		return api.SideEffectRead
	case cmdreflect.TierWriteLocal, cmdreflect.TierWriteShared:
		return api.SideEffectWrite
	case cmdreflect.TierDestructiveLocal, cmdreflect.TierDestructiveShared:
		return api.SideEffectDestructive
	case cmdreflect.TierInteractive:
		return api.SideEffectInteractive
	}
	// An unresolved tier is treated as a write: it is the
	// conservative read, and it never yields a method that claims
	// the call is safe.
	return api.SideEffectWrite
}

// bridgeExecutor runs a projected command through the cmdsurface
// bridge, so safety level, permissions, and confirmation are enforced
// by the same gate every other surface uses.
type bridgeExecutor struct {
	bridge *cmdsurface.Bridge
}

// Execute implements api.CommandExecutor.
//
// The request's provenance becomes the bridge's Meta unchanged: the
// principal and tenant the auth middleware established, the request
// id the middleware issued, the trace id and idempotency key the
// caller propagated. ctx is the request's own, so a client that
// disconnects cancels the command.
func (e *bridgeExecutor) Execute(
	ctx context.Context, req api.CommandRequest,
) (api.CommandResult, error) {
	if e.bridge == nil {
		return api.CommandResult{}, errors.New("cli: no command bridge configured")
	}

	inv := cmdsurface.Invocation{
		Path:  req.Path,
		Args:  req.Args,
		Flags: req.Flags,
		Meta:  metaFromRequest(req.Meta),
	}
	res, err := e.bridge.Invoke(ctx, inv)
	if err != nil {
		return api.CommandResult{}, translateBridgeError(err)
	}
	return api.CommandResult{
		ExitCode: res.ExitCode,
		Data:     res.Data,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}, nil
}

// metaFromRequest maps the HTTP layer's provenance onto the bridge's
// Meta. Scopes travel in Extra, comma-joined, because Meta has no
// typed field for entitlements and the permission gate is the one
// consumer.
func metaFromRequest(m api.RequestMeta) cmdsurface.Meta {
	meta := cmdsurface.Meta{
		Caller:         m.Principal,
		Tenant:         m.Tenant,
		Surface:        cmdsurface.SurfaceREST,
		RequestID:      m.RequestID,
		TraceID:        m.TraceID,
		IdempotencyKey: m.IdempotencyKey,
		RequestedAt:    m.ReceivedAt,
	}
	extra := map[string]string{}
	if m.RemoteAddr != "" {
		extra["remote_addr"] = m.RemoteAddr
	}
	if len(m.Scopes) > 0 {
		extra["scopes"] = strings.Join(m.Scopes, ",")
	}
	if len(extra) > 0 {
		meta.Extra = extra
	}
	return meta
}

// translateBridgeError maps the bridge's sentinels onto the
// projection's, so the HTTP layer switches on its own vocabulary
// rather than importing the bridge's.
func translateBridgeError(err error) error {
	switch {
	case errors.Is(err, cmdsurface.ErrUnknownCommand),
		errors.Is(err, cmdsurface.ErrSurfaceNotEnabled),
		errors.Is(err, cmdsurface.ErrNotInvocable):
		// Interactive and self-hosting commands are withheld at mount,
		// so the bridge's own gate is unreachable over REST; mapping
		// it keeps the vocabulary whole should a route ever exist.
		return fmt.Errorf("%w: %s", api.ErrCommandNotInvocable, err.Error())
	case errors.Is(err, cmdsurface.ErrDestructiveBlocked):
		return fmt.Errorf("%w: %s", api.ErrDestructiveBlocked, err.Error())
	case errors.Is(err, cmdsurface.ErrPermissionDenied):
		return fmt.Errorf("%w: %s", api.ErrPermissionDenied, err.Error())
	}
	return err
}
