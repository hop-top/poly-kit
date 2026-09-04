package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
)

// ErrSurfaceNotEnabled is returned when an Invocation's Meta.Surface
// is not in the resolved leaf's enabled-surface set. Bridge.Expose /
// Hide / the YAML config control which surfaces a leaf accepts.
var ErrSurfaceNotEnabled = errors.New("cmdsurface: surface not enabled for command")

// ErrDestructiveBlocked is returned when the policy gate refuses an
// Invocation because the leaf is destructive and the surface is not
// in Policy.AllowDestructiveOn.
var ErrDestructiveBlocked = errors.New("cmdsurface: destructive command blocked on this surface")

// ErrPermissionDenied is returned when the [PermissionFunc] refuses
// an Invocation. The wrapped message carries the decision's stable
// reason. Transports map it to their own "forbidden" vocabulary
// (403 over HTTP, a DENIED wire code on the socket); it is distinct
// from ErrDestructiveBlocked because the two are answered by
// different gates and fixed by different people — the destructive
// ceiling by the deployment's policy, a permission denial by the
// caller's entitlement.
var ErrPermissionDenied = errors.New("cmdsurface: permission denied")

// ErrAuthRefused is the error a transport reports through
// [Bridge.Audit] when it refuses a request before the bridge is
// reached because the caller failed authentication. The bridge never
// returns it — authentication is the transport's own gate — but
// routing the refusal through the same sinks keeps the audit trail
// whole: one stream carries "not authenticated", "not permitted",
// and "ran", with the same provenance fields on each.
var ErrAuthRefused = errors.New("cmdsurface: authentication refused")

// idempotencyKeyFlag mirrors the kit-managed --idempotency-key flag
// name that go/console/cli auto-registers on conditional-idempotent
// leaves. Mirrored, not imported, for the same reason the annotation
// keys are: cli reaches this package, not the reverse.
const idempotencyKeyFlag = "idempotency-key"

// Bridge projects a cobra root onto many surfaces. It owns the
// Runner, the Policy, and the per-leaf enablement map. Surfaces
// hand decoded Invocations to Bridge.Invoke and receive Results;
// they iterate Bridge.Leaves at mount time to discover which
// commands they should expose.
//
// Sinks are the audit fan-out slot. FromConfig populates it from
// cfg.Telemetry (see config.go) and [WithSinks] adds adopter sinks.
// Bridge.Invoke emits to registered sinks for every refusal and for
// every execution on a remote surface (any surface other than
// SurfaceCLI and SurfaceLib), so a sink sees "refused" and "ran"
// with the same provenance whether or not the Runner was reached.
// Adopters wrapping their Runner with the sinkRunner pattern in
// README.md keep that path; it observes only invocations that reach
// the Runner, which is why refusals are emitted here.
type Bridge struct {
	root   *cobra.Command
	cfg    bridgeConfig
	leaves []*Leaf // depth-first leaf order
	byPath map[string]*Leaf
	tree   *cmdreflect.Tree
	sinks  SinkSet
	mu     sync.RWMutex
}

// Leaf is the per-command view surface implementations need. Path
// is the cobra path from root (without the root segment); Cmd is
// the resolved *cobra.Command; Class is the snapshot of safety
// annotations; Enabled is the surface allow-set under current
// configuration.
type Leaf struct {
	Path    []string
	Cmd     *cobra.Command
	Class   SafetyClass
	Enabled map[Surface]bool

	// Descriptor is the canonical reflection of this command, from
	// [hop.top/kit/go/ai/cmdreflect]. Surfaces that need richer
	// metadata than SafetyClass carries — declared args, flag
	// types and defaults, output schema, deprecation detail —
	// read it here instead of walking the cobra tree again.
	//
	// Always non-nil for a leaf the bridge discovered.
	Descriptor *cmdreflect.Descriptor
}

// PathKey returns the leaf path as a space-joined string (the form
// Bridge.Expose / Hide accept as exact match).
func (l *Leaf) PathKey() string { return strings.Join(l.Path, " ") }

// bridgeConfig is the internal options bag set by Option funcs.
type bridgeConfig struct {
	runner     Runner
	policy     Policy
	permission PermissionFunc
	sinks      SinkSet
}

// Option configures a Bridge at construction.
type Option func(*bridgeConfig)

// WithRunner installs r as the bridge's Runner. Default is
// InProcessRunner(root).
func WithRunner(r Runner) Option { return func(c *bridgeConfig) { c.runner = r } }

// WithPolicy installs p as the bridge's Policy. Default is
// DefaultPolicy().
func WithPolicy(p Policy) Option { return func(c *bridgeConfig) { c.policy = p } }

// WithPermission installs fn as the bridge's [PermissionFunc], the
// gate [Invoke] consults after the destructive ceiling and before
// the Runner on every surface. A nil fn keeps the default,
// [PermitAll].
func WithPermission(fn PermissionFunc) Option {
	return func(c *bridgeConfig) { c.permission = fn }
}

// WithSinks registers audit sinks on the bridge. [Invoke] emits to
// them for every refusal and for every execution on a remote
// surface; [Bridge.Audit] lets a transport report its own pre-flight
// refusals (failed authentication) through the same set. Repeated
// options append.
func WithSinks(specs ...SinkSpec) Option {
	return func(c *bridgeConfig) { c.sinks = append(c.sinks, specs...) }
}

// New returns a Bridge that projects root onto the surfaces a
// caller subsequently enables via Expose / Hide / config. Leaves
// are discovered once at construction; commands added to root after
// New is called are not visible to the bridge.
func New(root *cobra.Command, opts ...Option) *Bridge {
	cfg := bridgeConfig{policy: DefaultPolicy()}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.runner == nil {
		cfg.runner = InProcessRunner(root)
	}
	if cfg.permission == nil {
		cfg.permission = PermitAll
	}
	b := &Bridge{
		root:   root,
		cfg:    cfg,
		byPath: make(map[string]*Leaf),
		sinks:  append(SinkSet(nil), cfg.sinks...),
	}
	b.discover()
	return b
}

// discover reflects the cobra tree once and records the leaves the
// bridge will project, in depth-first order.
//
// Reflection is delegated to [hop.top/kit/go/ai/cmdreflect], which
// describes EVERY command and records a reason for each one it
// judges non-invocable. The bridge keeps the invocable ones as
// leaves; the reasons for the rest stay available through
// [Bridge.NonInvocable], so "this command is not on the bridge"
// has an answer instead of being a silent omission.
//
// Hidden and deprecated commands are excluded, which is the
// reflector's default. Expose can re-enable a leaf by exact path,
// but only among the ones discovered here — matching the behavior
// this method has always had.
func (b *Bridge) discover() {
	defaults := b.cfg.policy.resolvedDefaults()
	defaultSet := make(map[Surface]bool, len(defaults))
	for _, s := range defaults {
		defaultSet[s] = true
	}

	// Interactive and destructive commands stay discoverable: the
	// bridge gates them at Invoke time through Policy.Allowed,
	// which is a per-surface decision the reflector cannot make
	// for all surfaces at once.
	b.tree = cmdreflect.Reflect(
		b.root,
		cmdreflect.AllowInteractive(),
		cmdreflect.AllowReserved(),
	)

	for _, d := range b.tree.Invocable() {
		if d.IsRoot() || d.Surface.HasSubCommands {
			continue
		}
		enabled := make(map[Surface]bool, len(defaultSet))
		for k, v := range defaultSet {
			enabled[k] = v
		}
		leaf := &Leaf{
			Path:       append([]string(nil), d.Path[1:]...),
			Cmd:        d.Cmd,
			Class:      classFromDescriptor(d),
			Enabled:    enabled,
			Descriptor: d,
		}
		b.leaves = append(b.leaves, leaf)
		b.byPath[leaf.PathKey()] = leaf
	}
}

// NonInvocable returns the descriptors the bridge did NOT turn into
// leaves, each carrying the reason it was excluded. Capability
// endpoints that advertise "every command" render these alongside
// Leaves so an agent can tell "no such command" from "that command
// exists but is not reachable here".
func (b *Bridge) NonInvocable() []*cmdreflect.Descriptor {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.tree == nil {
		return nil
	}
	return b.tree.NonInvocable()
}

// Descriptors returns the complete reflection of the bridge's cobra
// tree, invocable commands and excluded ones alike.
func (b *Bridge) Descriptors() []*cmdreflect.Descriptor {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.tree == nil {
		return nil
	}
	return b.tree.Descriptors
}

// Leaves returns the bridge's leaves in depth-first discovery
// order. The returned slice is a read-only view; surfaces must not
// mutate the Enabled maps (use Expose/Hide instead).
func (b *Bridge) Leaves() []*Leaf {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*Leaf, len(b.leaves))
	copy(out, b.leaves)
	return out
}

// Expose enables surfaces on every leaf whose path matches pattern.
// Pattern forms:
//
//   - "widget add"  — exact path
//   - "widget *"    — every leaf under "widget"
//   - "*"           — every leaf
//
// When surfaces is empty Expose is a no-op. Returns the receiver
// for chaining.
func (b *Bridge) Expose(pattern string, surfaces ...Surface) *Bridge {
	return b.setSurfaces(pattern, surfaces, true)
}

// Hide disables surfaces on every leaf matching pattern. See
// Expose for pattern forms.
func (b *Bridge) Hide(pattern string, surfaces ...Surface) *Bridge {
	return b.setSurfaces(pattern, surfaces, false)
}

func (b *Bridge) setSurfaces(pattern string, surfaces []Surface, value bool) *Bridge {
	if len(surfaces) == 0 {
		return b
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, leaf := range b.leaves {
		if !matchPattern(pattern, leaf.Path) {
			continue
		}
		for _, s := range surfaces {
			leaf.Enabled[s] = value
		}
	}
	return b
}

// matchPattern reports whether path matches pattern. Patterns are
// space-separated segments; the final segment may be "*" to match
// any descendant, and a single "*" matches every leaf.
func matchPattern(pattern string, path []string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	pat := strings.Fields(pattern)
	if len(pat) == 0 {
		return false
	}
	// Wildcard tail: "a b *" matches any path with prefix [a,b].
	if pat[len(pat)-1] == "*" {
		prefix := pat[:len(pat)-1]
		if len(path) < len(prefix) {
			return false
		}
		for i, seg := range prefix {
			if path[i] != seg {
				return false
			}
		}
		return true
	}
	// Exact match.
	if len(pat) != len(path) {
		return false
	}
	for i, seg := range pat {
		if path[i] != seg {
			return false
		}
	}
	return true
}

// Invoke routes inv through the bridge: resolves the leaf, applies
// the gates in order, then delegates to the configured Runner. The
// gates, and the error each refusal returns:
//
//  1. Resolution — ErrUnknownCommand: inv.Path does not resolve.
//  2. Enablement — ErrSurfaceNotEnabled: leaf is not exposed on
//     inv.Meta.Surface.
//  3. Destructive ceiling — ErrDestructiveBlocked: leaf is
//     destructive and Policy disallows the surface.
//  4. Permission — ErrPermissionDenied: the [PermissionFunc] refused
//     this Meta for this leaf; the message carries its reason.
//
// Confirmation is deliberately not a gate here: it is the command's
// own flag and its own refusal, the same on every surface as on the
// CLI, so the Runner's Result carries it as an exit code.
//
// Every refusal, and every execution on a remote surface, is emitted
// to the bridge's sinks (see [WithSinks]) with the Meta the transport
// supplied. RequestedAt is stamped when the surface left it zero, so
// an audit record always has a timestamp. When Meta carries an
// IdempotencyKey and the leaf registers the kit-managed
// --idempotency-key flag, the key is forwarded as that flag unless
// the caller set it explicitly.
//
// The Runner's own errors are returned as-is (wrapped runners are
// responsible for their own error contracts).
func (b *Bridge) Invoke(ctx context.Context, inv Invocation) (Result, error) {
	if inv.Meta.Surface == "" {
		// Library callers may omit the field; treat as SurfaceLib so
		// in-process Invoke calls Just Work.
		inv.Meta.Surface = SurfaceLib
	}
	if inv.Meta.RequestedAt.IsZero() {
		inv.Meta.RequestedAt = time.Now()
	}
	surface := inv.Meta.Surface

	leaf, err := b.resolveLeaf(inv.Path)
	if err != nil {
		return Result{}, b.refuse(ctx, inv, err)
	}
	if !leaf.Enabled[surface] {
		return Result{}, b.refuse(ctx, inv, fmt.Errorf("%w: %s on %s",
			ErrSurfaceNotEnabled, leaf.PathKey(), surface))
	}
	if !b.cfg.policy.Allowed(leaf.Class, surface) {
		return Result{}, b.refuse(ctx, inv, fmt.Errorf("%w: %s on %s",
			ErrDestructiveBlocked, leaf.PathKey(), surface))
	}
	if dec := b.Permission(ctx, inv.Meta, leaf); !dec.Allowed {
		return Result{}, b.refuse(ctx, inv, fmt.Errorf("%w: %s on %s: %s",
			ErrPermissionDenied, leaf.PathKey(), surface, dec.Reason))
	}

	inv = forwardIdempotencyKey(inv, leaf)

	res, err := b.cfg.runner.Run(ctx, inv)
	if surface.remote() {
		b.Audit(ctx, inv, res, err)
	}
	return res, err
}

// refuse emits a pre-execution refusal to the sinks when the surface
// is remote and returns err unchanged, so every early return in
// Invoke reads the same.
func (b *Bridge) refuse(ctx context.Context, inv Invocation, err error) error {
	if inv.Meta.Surface.remote() {
		b.Audit(ctx, inv, Result{}, err)
	}
	return err
}

// forwardIdempotencyKey copies Meta.IdempotencyKey into the leaf's
// --idempotency-key flag when the leaf has one and the caller did not
// set it. The caller's Flags map is never mutated: a transport may
// reuse it.
func forwardIdempotencyKey(inv Invocation, leaf *Leaf) Invocation {
	if inv.Meta.IdempotencyKey == "" || leaf == nil || leaf.Cmd == nil {
		return inv
	}
	if leaf.Cmd.Flags().Lookup(idempotencyKeyFlag) == nil {
		return inv
	}
	if _, set := inv.Flags[idempotencyKeyFlag]; set {
		return inv
	}
	flags := make(map[string]any, len(inv.Flags)+1)
	for k, v := range inv.Flags {
		flags[k] = v
	}
	flags[idempotencyKeyFlag] = inv.Meta.IdempotencyKey
	inv.Flags = flags
	return inv
}

// Permission asks the bridge's [PermissionFunc] whether meta may
// invoke leaf. Surfaces consult it at mount time with a Meta that
// carries only the surface, and honor a refusal there only when the
// decision is CallerIndependent — a caller-specific verdict cannot
// be known before a caller exists.
func (b *Bridge) Permission(ctx context.Context, meta Meta, leaf *Leaf) PermissionDecision {
	if leaf == nil {
		return PermissionDecision{Allowed: true}
	}
	return b.cfg.permission(ctx, meta, leaf)
}

// Audit emits one record to the bridge's registered sinks. Invoke
// calls it for every refusal and every remote execution; a transport
// calls it directly for a refusal the bridge never sees — a failed
// authentication, reported with [ErrAuthRefused] — so one audit
// stream carries every verdict with the same provenance fields.
//
// Sinks are best-effort: their errors are dropped here, as the
// SinkSet contract already makes them non-fatal, and an audit sink
// must never turn a refusal into a different refusal.
func (b *Bridge) Audit(ctx context.Context, inv Invocation, res Result, err error) {
	sinks := b.Sinks()
	if len(sinks) == 0 {
		return
	}
	_ = sinks.Emit(ctx, inv, res, err)
}

// remote reports whether s is a surface other than the two local
// runtimes. Audit applies to remote surfaces: a CLI invocation is
// the operator's own act, and an in-process library call has no
// caller to attribute.
func (s Surface) remote() bool {
	return s != SurfaceCLI && s != SurfaceLib
}

// resolveLeaf returns the *Leaf for path, or ErrUnknownCommand.
func (b *Bridge) resolveLeaf(path []string) (*Leaf, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	key := strings.Join(path, " ")
	if leaf, ok := b.byPath[key]; ok {
		return leaf, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownCommand, joinPath(path))
}

// Runner exposes the configured Runner. Surfaces that need to call
// Stream (WS / SSE) reach the Runner directly through this getter
// after the bridge has applied the policy gate to the leaf.
func (b *Bridge) Runner() Runner {
	return b.cfg.runner
}

// Policy returns the active Policy. Surfaces consult it to render
// "would this leaf be allowed?" lists in capability endpoints.
func (b *Bridge) Policy() Policy { return b.cfg.policy }

// Sinks returns a copy of the bridge's registered SinkSet. The
// returned slice is safe to inspect and to pass to SinkSet.Emit;
// mutating it does not affect the bridge.
//
// FromConfig populates it with the telemetry sink and [WithSinks]
// adds adopter sinks. Callers wiring a sinkRunner can merge
// Bridge.Sinks() with their own specs.
func (b *Bridge) Sinks() SinkSet {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(SinkSet, len(b.sinks))
	copy(out, b.sinks)
	return out
}

// appendSink registers spec on the bridge. Internal helper for
// FromConfig and (potentially) future Expose-style sink builders.
// Not exported: the public Bridge surface is fluent and we don't
// want adopters constructing partial SinkSpecs by accident — the
// sinkRunner pattern stays the recommended path for adopter sinks.
func (b *Bridge) appendSink(spec SinkSpec) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sinks = append(b.sinks, spec)
}

// closableSink is the duck-typed close contract registered sinks
// may implement. TelemetrySink satisfies it; non-closable sinks
// (LogSink, FileSink, …) silently no-op during Bridge.Close. This
// is the minimum surface needed to flush the kit-telemetry drain
// goroutine on process shutdown.
type closableSink interface {
	Close(context.Context) error
}

// Close drains every registered sink that implements
// closableSink. Errors are collected and joined; the first
// returned error does not short-circuit the rest. Idempotent only
// to the extent each sink's own Close is idempotent —
// TelemetrySink.Close is.
//
// Bridge.Close does NOT close the cobra root or the Runner;
// adopters that own additional resources (HTTP servers, bus
// subscribers) close those separately. The single responsibility
// here is "flush the drain goroutines my sinks own".
func (b *Bridge) Close(ctx context.Context) error {
	b.mu.RLock()
	specs := make(SinkSet, len(b.sinks))
	copy(specs, b.sinks)
	b.mu.RUnlock()

	var errs []error
	for _, spec := range specs {
		if spec.Sink == nil {
			continue
		}
		c, ok := spec.Sink.(closableSink)
		if !ok {
			continue
		}
		if err := c.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
