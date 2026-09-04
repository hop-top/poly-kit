// Package transportsvc is the registration seam for transport
// services: the reusable half of "front the tool's command tree over
// a transport and run it under the serve lifecycle".
//
// A transport service is a [hop.top/kit/go/console/serve.Service]
// whose work is projecting the completed cobra tree onto one
// transport. MCP, RPC, SSE, a bus consumer, and the built-in socket
// service are all that same shape, and they differ only in how
// requests arrive and how responses leave. That difference is the
// [Transport] interface; everything else is centralized here.
//
// # What is centralized
//
//   - Reflection of the command tree, once, at start, through
//     [hop.top/kit/go/transport/cmdsurface] and
//     [hop.top/kit/go/ai/cmdreflect].
//   - The policy path: per-leaf surface enablement and the
//     destructive ceiling, applied to every invocation.
//   - Readiness, reported once after the transport binds, with the
//     bound address surfaced to the supervisor.
//   - Ordered, idempotent stop within the caller's budget.
//
// # Why this is not in go/console/serve
//
// The lifecycle contract lives in
// [hop.top/kit/go/console/serve], and this seam implements it. It
// cannot live there: the command-tree half of the seam reaches
// cmdsurface, which reaches cmdreflect, which reaches
// [hop.top/kit/go/console/cli], which registers services back into
// serve. The contract package stays free of the transport stack, and
// the seam sits above both.
//
// The normative specification is
// docs/contracts/serve-lifecycle.md §"Transport services".
package transportsvc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/cmdsurface"
)

// Transport is the half of a transport service that is genuinely
// transport-specific: how requests arrive and how responses leave.
// Everything else a transport service needs — reflecting the command
// tree, applying the policy gate, reporting readiness, stopping in
// order — is centralized in [NewTransportService] and is identical
// for every transport (contract §"Transport services").
//
// An implementation is expected to be small. MCP, RPC, SSE, a bus
// consumer, and the built-in socket transport differ only in Bind and
// Serve; none of them re-implements a lifecycle.
type Transport interface {
	// Bind acquires whatever can fail deterministically — a
	// listener, a socket file, a subscription — and returns the
	// address the service is reachable at, or "" when the transport
	// has none.
	//
	// Bind is the acquisition the readiness contract is about: the
	// service reports ready when Bind returns nil, and never
	// before (contract §"Readiness").
	Bind(ctx context.Context) (addr string, err error)

	// Serve accepts work until ctx is canceled or it fails,
	// dispatching each request through inv. Returning nil after
	// cancellation is a clean stop.
	//
	// Serve is called only after Bind has succeeded. The Invoker is
	// already policy-gated and already resolved against the
	// reflected command tree, so an implementation decodes its wire
	// format, calls inv, and encodes the result — it does not
	// consult annotations or a policy table of its own.
	Serve(ctx context.Context, inv Invoker) error

	// Close releases what Bind acquired: closes the listener,
	// unlinks the socket file, detaches the subscription. It is
	// called once, bounded by the service's stop timeout, and must
	// respect ctx rather than assume it will be allowed to finish
	// (contract §"Ordered stop").
	//
	// Close MUST make Serve return. A Close that leaves the listener
	// open leaves the process holding a port after `serve` has
	// reported it stopped.
	Close(ctx context.Context) error
}

// Invoker is the one call a [Transport] makes into the command tree.
// It is the bridge's Invoke with the surface already pinned, so a
// transport cannot accidentally invoke as a surface other than its
// own.
//
// The returned error is the bridge's: [cmdsurface.ErrUnknownCommand]
// for a path that does not resolve, [cmdsurface.ErrSurfaceNotEnabled]
// for a leaf not exposed on this surface, and
// [cmdsurface.ErrDestructiveBlocked] for a destructive leaf the
// policy refuses. A transport maps them onto its wire format; it does
// not decide them.
type Invoker func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)

// TransportOption configures a transport service at construction.
type TransportOption func(*transportConfig)

type transportConfig struct {
	bridgeOpts []cmdsurface.Option
	expose     []exposeRule
	validate   func() error
	class      func() (string, string)
	dependsOn  []string
}

type exposeRule struct {
	pattern string
	on      bool
}

// WithBridgeOptions passes options through to the [cmdsurface.Bridge]
// built at start — a custom Runner, a custom Policy.
func WithBridgeOptions(opts ...cmdsurface.Option) TransportOption {
	return func(c *transportConfig) { c.bridgeOpts = append(c.bridgeOpts, opts...) }
}

// Expose enables the service's surface on every leaf matching
// pattern, in the pattern language of [cmdsurface.Bridge.Expose]
// ("widget add", "widget *", "*").
//
// Without any Expose or Hide, the bridge's default enablement
// applies, which does not include most non-CLI surfaces: a transport
// that wants to reach the whole tree says so.
func Expose(pattern string) TransportOption {
	return func(c *transportConfig) { c.expose = append(c.expose, exposeRule{pattern, true}) }
}

// Hide disables the service's surface on every leaf matching pattern.
// Rules apply in the order given, so a broad Expose followed by a
// narrow Hide carves an exception out of it.
func Hide(pattern string) TransportOption {
	return func(c *transportConfig) { c.expose = append(c.expose, exposeRule{pattern, false}) }
}

// WithValidate installs the service's [serve.Validator] gate — the second
// of the three validation gates, run before anything binds. A
// transport whose configuration can be wrong (a path that does not
// parse, a file that must exist) states it here so the failure is a
// usage error at exit 2 rather than a start failure a second later.
func WithValidate(fn func() error) TransportOption {
	return func(c *transportConfig) { c.validate = fn }
}

// WithClass declares the service's kit/side-effect and kit/network
// values for the policy gate. A service that does not declare a class
// is unclassified and passes the gate.
func WithClass(sideEffect, network string) TransportOption {
	return func(c *transportConfig) {
		c.class = func() (string, string) { return sideEffect, network }
	}
}

// WithDependsOn declares the services that must start before this one
// (contract §"Ordering").
func WithDependsOn(names ...string) TransportOption {
	return func(c *transportConfig) { c.dependsOn = append(c.dependsOn, names...) }
}

// TransportService is a [serve.Service] that fronts a cobra command tree
// over one [Transport]. Construct one with [NewTransportService].
//
// It implements [serve.Validator], [serve.Addressed], [serve.Classified], and
// [serve.Dependent] as configured, so the supervisor's gates, readiness
// address, and ordering work without the transport implementing any
// of them.
type TransportService struct {
	name    string
	root    *cobra.Command
	surface cmdsurface.Surface
	tr      Transport
	cfg     transportConfig

	mu     sync.Mutex
	bridge *cmdsurface.Bridge
	addr   string
	up     bool
	closed bool
}

// NewTransportService returns the transport service named name,
// projecting the command tree rooted at root onto surface via tr.
//
// The name must satisfy [serve.ValidateName]; an invalid one panics, in the
// same class as a registry collision — it is a wiring bug in main,
// and a service that cannot be named cannot be selected, configured,
// or reported on.
//
// What the service centralizes, so no transport repeats it:
//
//   - Reflection. The command tree is reflected once at Start through
//     [cmdsurface.New], which delegates to
//     [hop.top/kit/go/ai/cmdreflect]. Reflecting at start rather than
//     at construction is deliberate: the tree is complete only after
//     every option has run and every subcommand is mounted, and a
//     transport that reflected at construction would serve whatever
//     subset existed when main happened to call it.
//   - The policy path. Every invocation goes through
//     [cmdsurface.Bridge.Invoke], which resolves the leaf, checks
//     per-leaf surface enablement, and applies the destructive
//     ceiling. A transport never reads an annotation.
//   - Readiness. Ready is reported once, after Bind returns, and the
//     bound address is carried to the supervisor through [serve.Addressed].
//   - Stop. Close is called once and is idempotent from the caller's
//     side; a second Stop after a clean stop is a no-op rather than a
//     double close.
func NewTransportService(
	name string,
	root *cobra.Command,
	surface cmdsurface.Surface,
	tr Transport,
	opts ...TransportOption,
) *TransportService {
	if err := serve.ValidateName(name); err != nil {
		panic(err.Error())
	}
	if tr == nil {
		panic("transportsvc: NewTransportService called with nil Transport")
	}
	var cfg transportConfig
	for _, o := range opts {
		o(&cfg)
	}
	return &TransportService{name: name, root: root, surface: surface, tr: tr, cfg: cfg}
}

// Name implements [serve.Service].
func (t *TransportService) Name() string { return t.name }

// Validate implements [serve.Validator], delegating to the configured hook.
func (t *TransportService) Validate() error {
	if t.cfg.validate == nil {
		return nil
	}
	return t.cfg.validate()
}

// Class implements [serve.Classified] when a class was declared. A service
// with no declared class returns two empty strings, which the policy
// gate treats as unclassified.
func (t *TransportService) Class() (sideEffect, network string) {
	if t.cfg.class == nil {
		return "", ""
	}
	return t.cfg.class()
}

// DependsOn implements [serve.Dependent].
func (t *TransportService) DependsOn() []string { return t.cfg.dependsOn }

// Start reflects the command tree, binds the transport, reports
// ready, and serves until ctx is canceled.
func (t *TransportService) Start(ctx context.Context, ready func()) error {
	if t.root == nil {
		return errors.New("transportsvc: no command root")
	}

	// Reflect now, not at construction: the tree is complete only
	// once every option has mounted its commands.
	bridge := cmdsurface.New(t.root, t.cfg.bridgeOpts...)
	for _, rule := range t.cfg.expose {
		if rule.on {
			bridge.Expose(rule.pattern, t.surface)
		} else {
			bridge.Hide(rule.pattern, t.surface)
		}
	}

	addr, err := t.tr.Bind(ctx)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	t.mu.Lock()
	t.bridge = bridge
	t.addr = addr
	t.up = true
	t.mu.Unlock()

	// Bind succeeded: every acquisition that can fail
	// deterministically has succeeded, so the service is ready.
	ready()

	return t.tr.Serve(ctx, t.invoke)
}

// invoke is the [Invoker] handed to the transport. It pins the
// surface so a transport cannot invoke as another one, and routes
// through the bridge so the policy gate is never bypassed.
func (t *TransportService) invoke(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	t.mu.Lock()
	bridge := t.bridge
	t.mu.Unlock()
	if bridge == nil {
		return cmdsurface.Result{}, errors.New("transportsvc: not started")
	}
	inv.Meta.Surface = t.surface
	return bridge.Invoke(ctx, inv)
}

// Ready implements [serve.Service].
func (t *TransportService) Ready() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.up
}

// Addr implements [serve.Addressed]: the address Bind resolved, which is
// how an operator learns the socket path or the port the kernel
// picked for a wildcard address.
func (t *TransportService) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.addr
}

// Stop closes the transport within the caller's budget. A second Stop
// is a no-op: the supervisor stops a service once, but a transport
// that also closes on its own must not be closed twice.
func (t *TransportService) Stop(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.up = false
	t.mu.Unlock()

	return t.tr.Close(ctx)
}

// Bridge returns the bridge built at Start, or nil before it. Tests
// and capability endpoints that need to enumerate what the transport
// exposes — including the commands it excluded and why, through
// [cmdsurface.Bridge.NonInvocable] — read it here.
func (t *TransportService) Bridge() *cmdsurface.Bridge {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bridge
}

// Compile-time proof that a TransportService satisfies the lifecycle
// contract and every optional declaration the supervisor consults. A
// transport that stops satisfying one of these silently loses its
// gate, its address, or its ordering, so the assertion is the test.
var (
	_ serve.Service    = (*TransportService)(nil)
	_ serve.Validator  = (*TransportService)(nil)
	_ serve.Addressed  = (*TransportService)(nil)
	_ serve.Classified = (*TransportService)(nil)
	_ serve.Dependent  = (*TransportService)(nil)
)
