package cli

import (
	"context"
	"net"
	"strings"
	"sync"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

// serveAuthState is what every kit-shipped transport service shares
// on the security side: the adopter's permission gate, the audit
// sinks, and the extra bridge options tests inject. It lives on the
// Root so the api and socket services, and any service registered
// later, resolve the same values at Start.
type serveAuthState struct {
	permission cmdsurface.PermissionFunc
	sinks      cmdsurface.SinkSet
	// bridgeOpts are applied last on every kit-shipped service's
	// bridge. Not exposed: tests use it to install a stub Runner
	// behind the real serve path.
	bridgeOpts []cmdsurface.Option
}

// WithPermission installs the permission gate every kit-shipped
// transport service consults before running a command. It runs in
// [cmdsurface.Bridge.Invoke] after the destructive ceiling and
// before the command, on the api service and the socket service
// alike, so a caller is answered the same way whichever transport
// carried the call.
//
// The gate composes with the tool's policy engine: a --policy that
// refuses a command's side-effect class refuses it here too, for
// every caller, before fn is asked. fn then decides for this caller
// — typically by checking the credential's scopes
// (Meta.Extra["scopes"] over REST) against the leaf's
// kit/permissions annotation (leaf.Class.Permissions).
//
// Without this option every authenticated caller may run every
// command the policy permits, which is the behavior tools had before
// the gate existed.
func WithPermission(fn cmdsurface.PermissionFunc) func(*Root) {
	return func(r *Root) { r.serveAuth.permission = fn }
}

// WithAuditSinks registers audit sinks on every kit-shipped transport
// service. Each receives one record per refusal — authentication,
// surface enablement, the destructive ceiling, the permission gate —
// and one per command executed over a remote surface, carrying the
// principal, tenant, request id, trace id, surface, command path,
// and the verdict (the refusal's error, or the command's exit code).
//
// Sinks are best-effort and never change a verdict. See
// [cmdsurface.SinkSpec] for the filters, and [cmdsurface.FileSink],
// [cmdsurface.LogSink], [cmdsurface.WebhookSink], and
// [cmdsurface.BusSink] for ready-made destinations.
func WithAuditSinks(specs ...cmdsurface.SinkSpec) func(*Root) {
	return func(r *Root) { r.serveAuth.sinks = append(r.serveAuth.sinks, specs...) }
}

// serveBridgeOptions returns the bridge options every kit-shipped
// transport service applies at Start: the composed permission gate,
// the audit sinks, and any test-injected options. It is resolved at
// Start, not at registration, because --policy is parsed and adopter
// options run only after the service was constructed.
func (r *Root) serveBridgeOptions() ([]cmdsurface.Option, error) {
	perm, err := r.servePermission()
	if err != nil {
		return nil, err
	}
	opts := []cmdsurface.Option{
		cmdsurface.WithPermission(perm),
		cmdsurface.WithSinks(r.serveAuth.sinks...),
	}
	return append(opts, r.serveAuth.bridgeOpts...), nil
}

// servePermission builds the permission gate the services share. The
// policy engine's verdict comes first and is caller-independent: it
// answers the same question wrapPolicyRunE asks on the CLI, from the
// same --policy, so a command the policy refuses is refused on every
// surface for everyone and discovery can say so at mount. The
// adopter's gate runs second, for the caller-specific answer.
func (r *Root) servePermission() (cmdsurface.PermissionFunc, error) {
	engine, err := r.newPolicyEngine(r.Cmd)
	if err != nil {
		return nil, err
	}
	policyGate := permissionFromEngine(engine)
	adopter := r.serveAuth.permission
	if adopter == nil {
		return policyGate, nil
	}
	return func(ctx context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if dec := policyGate(ctx, meta, leaf); !dec.Allowed {
			return dec
		}
		return adopter(ctx, meta, leaf)
	}, nil
}

// permissionFromEngine adapts the policy engine to the bridge's gate.
// Engine.Authorize reads only the command's annotations and the
// loaded policy, so its verdict holds for every caller; the decision
// says so, which is what lets discovery withhold the command at mount
// rather than mount a route that can only refuse.
//
// The engine is guarded by a mutex because it is documented as
// unsafe for concurrent use, and a transport service answers
// requests concurrently.
func permissionFromEngine(engine *policy.Engine) cmdsurface.PermissionFunc {
	var mu sync.Mutex
	return func(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if engine == nil || leaf == nil || leaf.Cmd == nil {
			return cmdsurface.PermissionDecision{Allowed: true}
		}
		mu.Lock()
		allowed, _, reason := engine.Authorize(leaf.Cmd)
		mu.Unlock()
		if allowed {
			return cmdsurface.PermissionDecision{Allowed: true}
		}
		return cmdsurface.PermissionDecision{
			Reason:            reason,
			CallerIndependent: true,
		}
	}
}

// isLoopbackAddr reports whether a host:port listen address binds a
// loopback interface only. An empty host binds every interface and is
// not loopback; the literal "localhost" is accepted by name because
// it is the address every guide and script uses for the same intent.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	// An IPv6 zone ("::1%lo0") is not part of the address.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// leafByKey indexes a bridge's leaves by path key, so a projection
// building one descriptor per command resolves each leaf in constant
// time rather than rescanning the list per command.
func leafByKey(b *cmdsurface.Bridge) map[string]*cmdsurface.Leaf {
	if b == nil {
		return nil
	}
	leaves := b.Leaves()
	out := make(map[string]*cmdsurface.Leaf, len(leaves))
	for _, leaf := range leaves {
		out[leaf.PathKey()] = leaf
	}
	return out
}

// servePolicyConfigured reports whether a delegation policy is in
// force for this invocation — that is, whether the permission gate
// the transport services share can refuse anything at all.
//
// It asks the flag rather than the engine because a "no policy"
// engine is not nil: newPolicyEngine always returns one, and with
// --policy unset its Allow map is nil, which Authorize default-
// permits for every side-effect class, destructive included. A nil
// engine and a --policy-less engine are equally toothless, so the
// question that separates them is whether a policy was named at all.
//
// --policy is a persistent root flag bound to viper, so a config
// file that sets it counts exactly as the command line does.
func (r *Root) servePolicyConfigured() bool {
	if r == nil || r.Cmd == nil {
		return false
	}
	return strings.TrimSpace(flagValue(r.Cmd, policyFlag)) != ""
}
