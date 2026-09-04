package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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

// Bridge projects a cobra root onto many surfaces. It owns the
// Runner, the Policy, and the per-leaf enablement map. Surfaces
// hand decoded Invocations to Bridge.Invoke and receive Results;
// they iterate Bridge.Leaves at mount time to discover which
// commands they should expose.
//
// Sinks are an opt-in fan-out slot. FromConfig populates the slot
// from cfg.Telemetry (see config.go). Adopters using the manual
// path continue to wrap their Runner with the sinkRunner pattern
// documented in README.md — Bridge.Invoke does NOT auto-emit to
// sinks today, the slot is a registry callers fetch via
// Bridge.Sinks. This shape preserves the foundation contract while
// giving the kit-telemetry sink a structured home.
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
	runner Runner
	policy Policy
}

// Option configures a Bridge at construction.
type Option func(*bridgeConfig)

// WithRunner installs r as the bridge's Runner. Default is
// InProcessRunner(root).
func WithRunner(r Runner) Option { return func(c *bridgeConfig) { c.runner = r } }

// WithPolicy installs p as the bridge's Policy. Default is
// DefaultPolicy().
func WithPolicy(p Policy) Option { return func(c *bridgeConfig) { c.policy = p } }

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
	b := &Bridge{
		root:   root,
		cfg:    cfg,
		byPath: make(map[string]*Leaf),
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
// the policy gate, then delegates to the configured Runner. Errors
// are:
//
//   - ErrUnknownCommand: inv.Path does not resolve.
//   - ErrSurfaceNotEnabled: leaf is not exposed on inv.Meta.Surface.
//   - ErrDestructiveBlocked: leaf is destructive and Policy disallows
//     the surface.
//
// The Runner's own errors are returned as-is (wrapped runners are
// responsible for their own error contracts).
func (b *Bridge) Invoke(ctx context.Context, inv Invocation) (Result, error) {
	leaf, err := b.resolveLeaf(inv.Path)
	if err != nil {
		return Result{}, err
	}
	surface := inv.Meta.Surface
	if surface == "" {
		// Library callers may omit the field; treat as SurfaceLib so
		// in-process Invoke calls Just Work.
		surface = SurfaceLib
	}
	if !leaf.Enabled[surface] {
		return Result{}, fmt.Errorf("%w: %s on %s",
			ErrSurfaceNotEnabled, leaf.PathKey(), surface)
	}
	if !b.cfg.policy.Allowed(leaf.Class, surface) {
		return Result{}, fmt.Errorf("%w: %s on %s",
			ErrDestructiveBlocked, leaf.PathKey(), surface)
	}
	return b.cfg.runner.Run(ctx, inv)
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
// FromConfig is the canonical populator (telemetry today; other
// sink types remain adopter-wired). Callers wiring a sinkRunner
// can merge Bridge.Sinks() with their own specs.
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
