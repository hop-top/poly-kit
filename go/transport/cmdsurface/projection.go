package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/transport/api"
)

// ReasonWithheldByConfig is the discovery reason for a command whose
// leaf is not exposed on SurfaceREST: the deployment chose to keep it
// off the projection ([Bridge.Hide], or never exposing it).
//
// The projection owns this reason rather than the reflector: the
// reflector's vocabulary answers "is this command projectable at
// all", and the answer here is yes — this deployment chose not to.
// The spelling follows the reflector's hyphenated-lowercase
// convention so a client switching on the field sees one vocabulary.
const ReasonWithheldByConfig = "withheld-by-config"

// ProjectionOption configures [Projection] and [MountProjection].
type ProjectionOption func(*projectionConfig)

type projectionConfig struct {
	toolName    string
	toolVersion string
	reserved    ReservedLookup
	stopping    <-chan struct{}
	heartbeat   time.Duration
	auth        api.AuthFunc
	routerAuth  bool
	maxBody     int64
}

// ReservedLookup reports whether a depth-1 command name is one the
// tool reserves for itself (kit's own verbs). A kit root
// ([hop.top/kit/go/console/cli.Root]) satisfies it.
type ReservedLookup interface {
	IsReserved(name string) bool
}

// WithProjectionTool labels the discovery document with the tool's
// name and version.
func WithProjectionTool(name, version string) ProjectionOption {
	return func(c *projectionConfig) {
		c.toolName = name
		c.toolVersion = version
	}
}

// WithProjectionReserved names the tool's reserved verbs, which the
// projection describes and withholds with the reflector's "reserved"
// reason. A bare cobra tree has none; a kit root passes itself.
func WithProjectionReserved(r ReservedLookup) ProjectionOption {
	return func(c *projectionConfig) { c.reserved = r }
}

// WithProjectionStopping ends every open stream once ch is closed:
// the command is canceled and the terminal frame is a 503
// shutting_down error. The server's owner closes ch when it begins to
// stop, so a drain is not held by a stream with no end of its own.
// See [api.ProjectionConfig].Stopping.
func WithProjectionStopping(ch <-chan struct{}) ProjectionOption {
	return func(c *projectionConfig) { c.stopping = ch }
}

// WithProjectionHeartbeat sets the keep-alive interval on streaming
// routes. Zero keeps [api.DefaultStreamHeartbeat].
func WithProjectionHeartbeat(d time.Duration) ProjectionOption {
	return func(c *projectionConfig) { c.heartbeat = d }
}

// WithProjectionAuth authenticates every projection route — the
// command routes, their streams and the discovery listing — with fn,
// through [api.Auth]. A refused request is answered 401 and recorded
// in the bridge's sinks as [ErrAuthRefused] (see
// [ProjectionAuthRefusal]). The claims fn returns become the caller's
// principal, tenant and scopes on the bridge's Meta.
//
// MountProjection only; [Projection] mounts nothing.
func WithProjectionAuth(fn api.AuthFunc) ProjectionOption {
	return func(c *projectionConfig) { c.auth = fn }
}

// WithProjectionMaxBodyBytes caps each projection request body — the
// command routes and their streams — at n bytes, through
// [api.BodyLimit]. A body over the cap is answered 413 body_too_large
// and recorded in the bridge's sinks as [ErrBodyTooLarge] against the
// command the URL addresses (see [ProjectionBodyTooLarge]). Zero keeps
// [api.DefaultMaxBodyBytes] (1 MiB); a negative n mounts no cap, for
// a router that caps bodies itself.
//
// MountProjection only; [Projection] mounts nothing.
func WithProjectionMaxBodyBytes(n int64) ProjectionOption {
	return func(c *projectionConfig) { c.maxBody = n }
}

// WithProjectionRouterAuth declares that the router decides who may
// call the projection: its own [api.Auth] middleware, when it has
// one. It lifts MountProjection's refusal to mount a command declaring
// kit/auth-required without [WithProjectionAuth].
//
// It vouches for nobody. A command declaring kit/auth-required still
// runs only for a request an [api.Auth] verified; without one it is
// refused per call, 401 unauthenticated, loopback included — a
// loopback listener is reachable by every local user.
//
// The kit root's api service passes it: it installs router-wide auth
// itself when configured, and refuses a non-loopback address without
// it.
func WithProjectionRouterAuth() ProjectionOption {
	return func(c *projectionConfig) { c.routerAuth = true }
}

func newProjectionConfig(opts []ProjectionOption) projectionConfig {
	var cfg projectionConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

// Projection reflects b's cobra tree into the command projection
// [api.MountCommandProjection] serves under /v1/commands, executed by
// b. It mounts nothing; [MountProjection] is the one-call form.
//
// Every command is described, invocable or not. A command is served
// only when the reflector allows it on a projected surface AND the
// bridge would admit it on SurfaceREST: its leaf is exposed there
// (otherwise [ReasonWithheldByConfig]), policy's destructive ceiling
// allows REST (otherwise the reflector's unauthorized-destructive),
// and the permission gate does not refuse it for every caller
// (otherwise [ReasonPermissionDenied]). Interactive and reserved
// commands are described with their reason and never served.
//
// Calls run through [Bridge.Invoke] with Meta.Surface = SurfaceREST
// and the request's provenance (principal, tenant, scopes, request
// id, trace id, idempotency key); streams are admitted with
// [Bridge.Admit] before the response commits, so a refusal is a
// status, not a frame.
func Projection(b *Bridge, opts ...ProjectionOption) (api.ProjectionConfig, error) {
	if b == nil {
		return api.ProjectionConfig{}, errors.New("cmdsurface: Projection: nil Bridge")
	}
	return buildProjection(b, newProjectionConfig(opts)), nil
}

// buildProjection is Projection past its argument check.
func buildProjection(b *Bridge, cfg projectionConfig) api.ProjectionConfig {
	// No Allow* options. Each one makes a class of command
	// INVOCABLE, not merely described — the reflector describes
	// every command unconditionally. Allowing interactive commands
	// would mount a shell over HTTP; the descriptors still appear in
	// discovery carrying their reason.
	var ropts []cmdreflect.Option
	if cfg.reserved != nil {
		ropts = append(ropts, cmdreflect.WithReserved(cfg.reserved))
	}
	tree := cmdreflect.Reflect(b.root, ropts...)

	pcfg := api.ProjectionConfig{
		ToolName:        cfg.toolName,
		ToolVersion:     cfg.toolVersion,
		Executor:        &projectionExecutor{bridge: b},
		StreamHeartbeat: cfg.heartbeat,
		Stopping:        cfg.stopping,
	}
	leaves := leafByKey(b)
	for _, d := range tree.Descriptors {
		// The root itself and pure command groups are not calls.
		if d.IsRoot() || d.Surface.HasSubCommands {
			continue
		}
		pcfg.Descriptors = append(pcfg.Descriptors,
			descriptorToProjection(d, b, leaves[d.PathKey()]))
	}
	pcfg.Personalize = callerListing(b, leaves)
	return pcfg
}

// callerListing is the projection's [api.ProjectionConfig.Personalize]:
// for a request whose caller the router's [api.Auth] verified, each
// command the shared listing serves is re-asked of the permission gate
// (slot 6) for that caller, through [Bridge.Verdict], so the caller
// sees what it may run. An anonymous request gets the shared listing.
func callerListing(b *Bridge, leaves map[string]*Leaf) func(*http.Request) func(api.CommandDescriptor) string {
	return func(r *http.Request) func(api.CommandDescriptor) string {
		meta := metaFromRequest(api.RequestMetaFrom(r))
		if !meta.Authenticated() {
			return nil
		}
		ctx := r.Context()
		return func(d api.CommandDescriptor) string {
			return verdictReason(b.Verdict(ctx, meta, leaves[d.PathKey()]))
		}
	}
}

// verdictReason is the discovery reason of a [Bridge.Verdict]
// refusal, "" for none.
func verdictReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInsufficientScope):
		return ReasonInsufficientScope
	default:
		return ReasonPermissionDenied
	}
}

// MountProjection mounts b's command projection on r: one route per
// served command under /v1/commands at the method its side-effect
// class selects, a streaming twin at <route>/stream, the discovery
// listing at GET /v1/commands, and the OpenAPI description — added
// to the router's spec when it was built with [api.WithOpenAPI], a
// minimal spec at /openapi.json otherwise. This is the REST surface a
// kit root's api service serves, for a bridge over a bare cobra tree.
//
// The wire format is the command projection's; see [Projection] for
// what is served and [api.MountCommandProjection] for the routes.
// Expose the leaves first: the default enabled set leaves REST off,
// and a leaf not exposed there is described as
// [ReasonWithheldByConfig].
//
//	b := cmdsurface.New(root)
//	b.Expose("*", cmdsurface.SurfaceREST)
//	err := cmdsurface.MountProjection(b, r,
//	    cmdsurface.WithProjectionTool("widgets", version),
//	    cmdsurface.WithProjectionAuth(verifyBearer))
//
// Request bodies are capped at 1 MiB unless
// [WithProjectionMaxBodyBytes] says otherwise.
//
// Authentication is the router's or [WithProjectionAuth]'s; the
// projection installs none otherwise. A served command that declares
// kit/auth-required is therefore refused at mount unless one of
// [WithProjectionAuth] or [WithProjectionRouterAuth] says who
// authenticates it, and each call to it runs only when an
// [api.Auth] verified the request (401 unauthenticated otherwise).
func MountProjection(b *Bridge, r *api.Router, opts ...ProjectionOption) error {
	if b == nil {
		return errors.New("cmdsurface: MountProjection: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: MountProjection: nil api.Router")
	}
	cfg := newProjectionConfig(opts)
	pcfg := buildProjection(b, cfg)

	if cfg.auth == nil && !cfg.routerAuth {
		if d, ok := firstAuthRequired(pcfg); ok {
			return fmt.Errorf(
				"cmdsurface: MountProjection: %q declares kit/auth-required and nothing authenticates the projection; "+
					"pass WithProjectionAuth, or WithProjectionRouterAuth when the router authenticates",
				d.PathKey())
		}
	}

	// The routes go on a group so the body cap and WithProjectionAuth
	// wrap the projection and nothing else on the router. The group
	// shares the router's mux and middleware; the spec lives on the
	// root, which is where huma was configured. The cap runs before
	// authentication, as on every kit HTTP listener.
	var mws []api.Middleware
	if limit := api.MaxBodyBytesOrDefault(cfg.maxBody); limit > 0 {
		mws = append(mws, api.BodyLimit(limit,
			api.OnBodyTooLarge(ProjectionBodyTooLarge(b))))
	}
	if cfg.auth != nil {
		mws = append(mws, api.Auth(cfg.auth,
			api.OnAuthRefused(ProjectionAuthRefusal(b))))
	}
	target := r
	if len(mws) > 0 {
		target = r.Group("", mws...)
	}
	api.MountCommandProjection(target, pcfg)
	api.DescribeCommandProjection(r, pcfg)
	if api.HumaAPI(r) == nil {
		// Serves a floor spec only when huma does not own the path.
		api.MountMinimalProjectionSpec(target, pcfg)
	}
	return nil
}

// firstAuthRequired returns the first served descriptor declaring
// kit/auth-required.
func firstAuthRequired(pcfg api.ProjectionConfig) (api.CommandDescriptor, bool) {
	for _, d := range pcfg.Descriptors {
		if d.Invocable && d.AuthRequired {
			return d, true
		}
	}
	return api.CommandDescriptor{}, false
}

// ProjectionAuthRefusal returns the hook [api.OnAuthRefused] takes for
// a router serving b's projection. A request the auth middleware
// refuses reaches b's sinks as an invocation that never ran, wrapped
// in [ErrAuthRefused], carrying the request id, trace id, peer, HTTP
// method and path, and — when the URL addresses a projected command
// or its stream — that command's path. One audit stream then holds
// every verdict, refusals before the bridge included.
//
// [WithProjectionAuth] installs it. A router that authenticates for
// itself passes it to its own [api.Auth].
func ProjectionAuthRefusal(b *Bridge) func(r *http.Request, err error) {
	if b == nil {
		return func(*http.Request, error) {}
	}
	known := projectedPaths(b)
	return func(r *http.Request, err error) {
		meta := api.RequestMetaFrom(r)
		inv := Invocation{
			Path: projectedPathOf(r.URL.Path, known),
			Meta: Meta{
				Surface:     SurfaceREST,
				RequestID:   meta.RequestID,
				TraceID:     meta.TraceID,
				Traceparent: meta.Traceparent,
				Tracestate:  meta.Tracestate,
				RequestedAt: meta.ReceivedAt,
				Extra:       httpRefusalExtra(r, meta),
			},
		}
		b.Audit(r.Context(), inv, Result{},
			fmt.Errorf("%w: %v", ErrAuthRefused, err))
	}
}

// projectedPaths indexes every command the projection describes, by
// space-joined path: every command of the tree but the root and pure
// groups.
func projectedPaths(b *Bridge) map[string]bool {
	out := map[string]bool{}
	for _, d := range b.Descriptors() {
		if d.IsRoot() || d.Surface.HasSubCommands {
			continue
		}
		out[d.PathKey()] = true
	}
	return out
}

// projectedPathOf returns the command path a projected route
// addresses, or nil for any other URL.
//
// A streaming route addresses the command before its trailing
// "stream" segment. known — the projected commands, keyed by
// space-joined path — settles which reading applies: a command is
// only ever projected as a leaf, so a URL naming a known command is
// that command, and one whose path minus "stream" is known is that
// command's stream. An unknown URL keeps every segment.
func projectedPathOf(urlPath string, known map[string]bool) []string {
	prefix := api.CommandProjectionPrefix + "/"
	if !strings.HasPrefix(urlPath, prefix) {
		return nil
	}
	rest := strings.Trim(strings.TrimPrefix(urlPath, prefix), "/")
	if rest == "" {
		return nil
	}
	path := strings.Split(rest, "/")
	suffix := strings.TrimPrefix(api.StreamSuffix, "/")
	if n := len(path); n > 1 && path[n-1] == suffix &&
		!known[strings.Join(path, " ")] && known[strings.Join(path[:n-1], " ")] {
		return path[:n-1]
	}
	return path
}

// leafByKey indexes a bridge's leaves by path key, so a projection
// building one descriptor per command resolves each leaf in constant
// time rather than rescanning the list per command.
func leafByKey(b *Bridge) map[string]*Leaf {
	leaves := b.Leaves()
	out := make(map[string]*Leaf, len(leaves))
	for _, leaf := range leaves {
		out[leaf.PathKey()] = leaf
	}
	return out
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
func descriptorToProjection(d *cmdreflect.Descriptor, b *Bridge, leaf *Leaf) api.CommandDescriptor {
	out := api.CommandDescriptor{
		Path:                 append([]string(nil), d.Path[1:]...),
		Summary:              d.Short,
		Description:          d.Long,
		SideEffect:           sideEffectClass(d.Safety.Tier),
		SideEffectSource:     sideEffectSource(d.Safety),
		Invocable:            d.Invocable,
		Reason:               string(d.Reason),
		RequiresConfirmation: d.Safety.RequiresConfirmation,
		RequiresConfirmToken: d.Safety.DestructiveTokenRequired,
		AuthRequired:         d.Safety.AuthRequired,
		OutputSchema:         d.Output.Schema,
		// The spec declares ETag, Cache-Control and 304 only where the
		// result cache can answer: a store, and a read leaf with a TTL.
		Cacheable: b.rcache != nil && leaf != nil && leaf.cacheTTL > 0,
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
			// The deployment took this one off REST. A distinct
			// reason keeps "we chose not to" separable from "policy
			// forbids it" in an operator's listing.
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
			out.Reason = ReasonPermissionDenied
		}
	}
	return out
}

// restEnabled reports whether the leaf is exposed on REST.
//
// A descriptor with no leaf on the bridge is treated as enabled: the
// reflector already judged it invocable, and the absence means the
// bridge never discovered it, which policyAllowsREST answers for.
func restEnabled(leaf *Leaf) bool {
	if leaf == nil {
		return true
	}
	return leaf.Enabled[SurfaceREST]
}

// policyAllowsREST asks the bridge whether the leaf may be invoked
// over REST at all.
func policyAllowsREST(d *cmdreflect.Descriptor, b *Bridge) bool {
	return b.Policy().Allowed(SafetyClass{
		Destructive:  d.Safety.Destructive(),
		AuthRequired: d.Safety.AuthRequired,
	}, SurfaceREST)
}

// deniedForEveryone asks the permission gate with a Meta that names
// only the surface, and honors a refusal only when the gate says the
// verdict does not depend on the caller. Discovery cannot know who
// will call; it can only withhold what nobody may call.
func deniedForEveryone(b *Bridge, leaf *Leaf) bool {
	if leaf == nil {
		return false
	}
	dec := b.Permission(context.Background(), Meta{Surface: SurfaceREST}, leaf)
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
	case cmdreflect.TierUnannotated:
		// A command that declared nothing is projected as a write.
		// The HTTP axis has no "unknown" value, and write is the
		// weakest class whose method (POST) is non-safe and
		// non-idempotent — exactly the handling an undeclared
		// command warrants from a cache or a retrying client.
		return api.SideEffectWrite
	}
	// An unresolved tier is treated as a write: it is the
	// conservative read, and it never yields a method that claims
	// the call is safe.
	return api.SideEffectWrite
}

// sideEffectSource says whether the tier behind the projected class
// is the adopter's declaration or kit's stand-in. The heuristic is
// checked first: it sets a declared-looking tier (destructive-shared)
// that Tier.Declared alone would report as the adopter's word.
func sideEffectSource(s cmdreflect.Safety) api.SideEffectSource {
	switch {
	case s.TierInferred:
		return api.SideEffectSourceInferred
	case s.Tier == cmdreflect.TierUnannotated:
		return api.SideEffectSourceUnannotated
	case !s.Tier.Declared():
		// TierUnknown: an annotation was written and did not resolve.
		return api.SideEffectSourceMalformed
	}
	return api.SideEffectSourceDeclared
}

// projectionExecutor runs a projected command through the bridge, so
// safety level, permissions, and confirmation are enforced by the
// same gate every other surface uses.
type projectionExecutor struct {
	bridge *Bridge
}

// Execute implements api.CommandExecutor.
//
// The request's provenance becomes the bridge's Meta unchanged: the
// principal and tenant the auth middleware established, the request
// id the middleware issued, the trace id and idempotency key the
// caller propagated. ctx is the request's own, so a client that
// disconnects cancels the command.
//
// A read the result cache handles carries its [CacheInfo] out as the
// result's [api.CacheDirective], which the GET route renders as ETag
// and Cache-Control and answers If-None-Match against.
func (e *projectionExecutor) Execute(ctx context.Context, req api.CommandRequest) (api.CommandResult, error) {
	adm, err := e.bridge.Admit(ctx, projectionInvocation(req))
	if err != nil {
		return api.CommandResult{}, translateProjectionError(err)
	}
	res, err := adm.Run(ctx)
	if err != nil {
		return api.CommandResult{}, translateProjectionError(err)
	}
	out := api.CommandResult{
		ExitCode: res.ExitCode,
		Data:     res.Data,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		Replayed: res.Replayed,
	}
	if info, ok := adm.Cache(); ok {
		out.Cache = &api.CacheDirective{ETag: info.ETag, MaxAge: info.MaxAge, Private: info.Private}
	}
	return out, nil
}

// OpenStream implements api.CommandStreamer.
//
// The bridge's gates run here, through Admit, before the projection
// commits the response to a stream: a refusal returns the same
// translated error Execute returns, so the streaming route answers it
// with the same status. The call's place at the capacity gate is
// reserved here too, so an overload is a 503 rather than an error
// frame. The admitted invocation runs, and is audited, when the
// stream's Run is called; the streaming route always calls it.
func (e *projectionExecutor) OpenStream(ctx context.Context, req api.CommandRequest) (api.CommandStream, error) {
	adm, err := e.bridge.Admit(ctx, projectionInvocation(req))
	if err != nil {
		return nil, translateProjectionError(err)
	}
	if err := adm.Reserve(ctx); err != nil {
		return nil, translateProjectionError(err)
	}
	return projectionStream{adm: adm}, nil
}

// projectionStream runs one admitted invocation through the Runner's
// Stream, translating its events into the projection's.
type projectionStream struct {
	adm *Admission
}

// Replayed reports whether the stream replays a recorded answer, so
// the projection marks the response before it commits to the stream.
func (s projectionStream) Replayed() bool { return s.adm.Replayed() }

// Run implements api.CommandStream. The runner's terminal "done"
// event becomes the returned result rather than a frame; every other
// event is forwarded as it arrives.
func (s projectionStream) Run(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
	in := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- s.adm.Stream(ctx, in) }()

	var res Result
	for ev := range in {
		if ev.Kind == "done" {
			if r, ok := ev.Data.(*Result); ok && r != nil {
				res = *r
			}
			continue
		}
		events <- api.CommandEvent{Kind: ev.Kind, Data: ev.Data, At: ev.At}
	}
	err := <-errc
	return api.CommandResult{
		ExitCode: res.ExitCode,
		Data:     res.Data,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		Replayed: res.Replayed,
	}, err
}

// projectionInvocation builds the bridge invocation for a projected
// request.
func projectionInvocation(req api.CommandRequest) Invocation {
	return Invocation{
		Path:  req.Path,
		Args:  req.Args,
		Flags: req.Flags,
		Meta:  metaFromRequest(req.Meta),
	}
}

// metaFromRequest maps the HTTP layer's provenance onto the bridge's
// Meta. Scopes travel in Extra, comma-joined, because Meta has no
// typed field for entitlements and the permission gate is the one
// consumer. A request [api.Auth] verified is [EstablishedVerified];
// nothing else is.
func metaFromRequest(m api.RequestMeta) Meta {
	meta := Meta{
		Caller:         m.Principal,
		Tenant:         m.Tenant,
		Surface:        SurfaceREST,
		RequestID:      m.RequestID,
		TraceID:        m.TraceID,
		Traceparent:    m.Traceparent,
		Tracestate:     m.Tracestate,
		IdempotencyKey: m.IdempotencyKey,
		RequestedAt:    m.ReceivedAt,
	}
	if m.Authenticated {
		meta.Established = EstablishedVerified
	}
	extra := map[string]string{}
	if m.RemoteAddr != "" {
		extra["remote_addr"] = m.RemoteAddr
	}
	if m.PeerAddr != "" {
		extra["peer_addr"] = m.PeerAddr
	}
	if len(m.Scopes) > 0 {
		extra["scopes"] = strings.Join(m.Scopes, ",")
	}
	if len(extra) > 0 {
		meta.Extra = extra
	}
	return meta
}

// translateProjectionError maps the bridge's sentinels onto the
// projection's, so the HTTP layer switches on its own vocabulary
// rather than importing the bridge's.
func translateProjectionError(err error) error {
	switch {
	case errors.Is(err, ErrAuthRefused):
		return fmt.Errorf("%w: %s", api.ErrUnauthenticated, err.Error())
	case errors.Is(err, ErrUnknownCommand),
		errors.Is(err, ErrSurfaceNotEnabled),
		errors.Is(err, ErrNotInvocable):
		// Interactive and self-hosting commands are withheld at mount,
		// so the bridge's own gate is unreachable over REST; mapping
		// it keeps the vocabulary whole should a route ever exist.
		return fmt.Errorf("%w: %s", api.ErrCommandNotInvocable, err.Error())
	case errors.Is(err, ErrDestructiveBlocked):
		return fmt.Errorf("%w: %s", api.ErrDestructiveBlocked, err.Error())
	case errors.Is(err, ErrInsufficientScope):
		// Both chains stay: the projection's sentinel picks the
		// status, the bridge's error names the required scopes.
		return fmt.Errorf("%w: %w", api.ErrInsufficientScope, err)
	case errors.Is(err, ErrPermissionDenied):
		return fmt.Errorf("%w: %s", api.ErrPermissionDenied, err.Error())
	case errors.Is(err, ErrRateLimited):
		// Both chains stay: the projection's sentinel picks the
		// status, the bridge's error carries the retry hint.
		return fmt.Errorf("%w: %w", api.ErrRateLimited, err)
	case errors.Is(err, ErrIdempotencyConflict):
		return fmt.Errorf("%w: %s", api.ErrIdempotencyConflict, err.Error())
	case errors.Is(err, ErrIdempotencyKeyReused):
		return fmt.Errorf("%w: %s", api.ErrIdempotencyKeyReused, err.Error())
	case errors.Is(err, ErrOverloaded):
		return fmt.Errorf("%w: %w", api.ErrOverloaded, err)
	}
	return err
}
