package cli

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// serveAuthState is what every kit-shipped transport service shares
// on the security side: the adopter's permission gate, the audit
// sinks, and the extra bridge options tests inject. It lives on the
// Root so the api, socket, and mcp services, and any service registered
// later, resolve the same values at Start.
type serveAuthState struct {
	permission cmdsurface.PermissionFunc
	// rules compiles the active --policy's permissions: block; nil
	// until an evaluator is wired with WithPermissionRules.
	rules PermissionRuleCompiler
	sinks cmdsurface.SinkSet
	// chains holds the audit chains services.<svc>.audit.sinks
	// opened, shared by every service that names the same file.
	chains auditChains
	// idem is the idempotency ledger every service shares.
	idem serveIdempotencyState
	// bridgeOpts are applied last on every kit-shipped service's
	// bridge. Not exposed: tests use it to install a stub Runner
	// behind the real serve path.
	bridgeOpts []cmdsurface.Option
}

// WithPermission installs the permission gate every kit-shipped
// transport service consults before running a command. It runs in
// [cmdsurface.Bridge.Invoke] after the destructive ceiling and
// before the command, on the api, socket, and mcp services
// alike, so a caller is answered the same way whichever transport
// carried the call.
//
// fn is the last of the deciders, and each can only narrow. The
// bridge's built-in scope check runs first: a command declaring
// kit/permissions runs on a served surface only for a caller whose
// verified credential holds every scope it names, and is otherwise
// refused insufficient_scope (see [cmdsurface.ErrInsufficientScope]).
// Then the tool's policy engine: a --policy that refuses a command's
// side-effect class refuses it here too, for every caller. Then the
// policy's permissions: rules, when an evaluator is wired
// ([WithPermissionRules]). fn is asked last, for whatever else this
// caller may not do — a suspended account, a tenant boundary, a
// decision that needs your own data.
//
// Without this option every caller the scope check, the policy and
// its rules admit may run the command.
func WithPermission(fn cmdsurface.PermissionFunc) func(*Root) {
	return func(r *Root) { r.serveAuth.permission = fn }
}

// PermissionRuleCompiler turns the permissions: block of the active
// --policy into a permission gate. It validates and compiles every
// rule up front, and returns an error naming the first rule it cannot
// use; the service then refuses to start.
type PermissionRuleCompiler func(rules []policy.PermissionRule) (cmdsurface.PermissionFunc, error)

// WithPermissionRules installs the evaluator for the permissions:
// block of the tool's --policy file. The compiled rules join the
// permission gate of every kit-shipped transport service, after the
// scope check and the policy's allow lists and before the adopter's
// [WithPermission]; they can only narrow what those admitted.
//
// kit's evaluator is CEL, in go/console/cli/celpermission: pass
// celpermission.With() rather than calling this directly. It is
// separate so a tool that never serves rules does not link cel-go.
// A policy that declares rules while no evaluator is wired refuses
// to serve rather than serve without them.
func WithPermissionRules(compile PermissionRuleCompiler) func(*Root) {
	return func(r *Root) { r.serveAuth.rules = compile }
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
// the audit sinks — registered in code, then svc's audit.sinks list —
// with svc's audit.redact block, svc's per-command deadline default
// (timeouts.command), idempotency replay as svc's idempotency block
// sets it, and any test-injected options. It is resolved at Start, not at
// registration, because --policy is parsed and adopter options run
// only after the service was constructed.
func (r *Root) serveBridgeOptions(svc string) ([]cmdsurface.Option, error) {
	perm, err := r.servePermission()
	if err != nil {
		return nil, err
	}
	redaction, err := serveAuditRedaction(r.Viper, svc)
	if err != nil {
		return nil, err
	}
	configured, err := r.serveConfiguredAuditSinks(svc)
	if err != nil {
		return nil, err
	}
	commandTimeout, err := serveCommandTimeout(r.Viper, svc)
	if err != nil {
		return nil, err
	}
	idem, err := r.serveIdempotencyOptions(svc)
	if err != nil {
		return nil, err
	}
	capacity, err := r.serveConcurrencyOptions(svc)
	if err != nil {
		return nil, err
	}
	opts := []cmdsurface.Option{
		cmdsurface.WithPermission(perm),
		cmdsurface.WithSinks(r.serveAuth.sinks...),
		cmdsurface.WithSinks(configured...),
		cmdsurface.WithAuditRedaction(redaction),
		cmdsurface.WithCommandTimeout(commandTimeout),
	}
	opts = append(opts, idem...)
	opts = append(opts, capacity...)
	return append(opts, r.serveAuth.bridgeOpts...), nil
}

// servePermission builds the permission gate the services share. The
// policy engine's verdict comes first and is caller-independent: it
// answers the same question wrapPolicyRunE asks on the CLI, from the
// same --policy, so a command the policy refuses is refused on every
// surface for everyone and discovery can say so at mount. The policy's
// permissions: rules run second and the adopter's gate last, for the
// caller-specific answer. The first refusal stands: a later decider is
// never asked about a call an earlier one refused.
func (r *Root) servePermission() (cmdsurface.PermissionFunc, error) {
	engine, err := r.newPolicyEngine(r.Cmd)
	if err != nil {
		return nil, err
	}
	gates := []cmdsurface.PermissionFunc{permissionFromEngine(engine)}
	rules, err := r.servePermissionRules(engine.Policy())
	if err != nil {
		return nil, err
	}
	if rules != nil {
		gates = append(gates, rules)
	}
	if adopter := r.serveAuth.permission; adopter != nil {
		gates = append(gates, adopter)
	}
	if len(gates) == 1 {
		return gates[0], nil
	}
	return func(ctx context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		for _, gate := range gates {
			if dec := gate(ctx, meta, leaf); !dec.Allowed {
				return dec
			}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}, nil
}

// servePermissionRules compiles p's permissions: block with the wired
// evaluator; nil when p declares no rules. A rule that does not compile,
// or rules with no evaluator to run them, is a usage error: the service
// refuses to start rather than serve without the rules the operator
// named.
func (r *Root) servePermissionRules(p policy.Policy) (cmdsurface.PermissionFunc, error) {
	if len(p.Permissions) == 0 {
		return nil, nil
	}
	if r.serveAuth.rules == nil {
		return nil, output.UsageError(fmt.Sprintf(
			"policy %q declares permissions: rules, but this tool wires no rule evaluator (celpermission.With)", p.Name))
	}
	gate, err := r.serveAuth.rules(p.Permissions)
	if err != nil {
		return nil, output.UsageError(fmt.Sprintf("policy %q: %v", p.Name, err))
	}
	return gate, nil
}

// refuseAll is the permission gate of a bridge whose shared options
// could not be resolved: it refuses every call with why, so a service
// never runs a command without its gate and audit sinks.
func refuseAll(err error) cmdsurface.PermissionFunc {
	return func(context.Context, cmdsurface.Meta, *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		return cmdsurface.PermissionDecision{Reason: err.Error(), CallerIndependent: true}
	}
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

// The hooks below are package functions rather than Root methods on
// purpose: the linker keeps every exported method of a type that can
// reach reflection, so a method would link the serve bridge machinery
// into every kit CLI, served or not. A function nobody calls is
// dropped.

// ServeBridgeOptions returns the bridge options the kit-shipped
// transport service svc applies when it starts: the per-invocation
// runner when [WithRootFactory] is set (carrying the operator's
// replayed root flags), the invocation tracing and metrics of the
// provider [WithObservability] linked, then the composed permission
// gate ([WithPermission] after the --policy engine), the audit sinks
// ([WithAuditSinks], then svc's audit.sinks list) with svc's
// audit.redact block, the per-command deadline (timeouts.command),
// the capacity gate (services.<svc>.concurrency, on by default
// wherever the service listens), and any test-injected options. It is
// the same set the socket service's bridge gets, and it must be
// called at Start — --policy is parsed and every Root option has run
// only by then.
//
// On error the options returned refuse every call: a caller that
// cannot report the error builds its bridge from them rather than
// serve ungated or unaudited. [ValidateServeBridge] makes that path
// unreachable in practice.
//
// A service living outside this package — the MCP service in
// go/console/cli/mcpserve — builds its bridge from it, so it meets
// exactly the gates the built-in services do.
//
// The rate limit (services.<svc>.rate_limit) takes its beyond-loopback
// default here, on unless configured off: a service that does not say
// where it listens is not assumed to be local. A service that knows
// uses [ServeBridgeOptionsFor].
func ServeBridgeOptions(r *Root, svc string) ([]cmdsurface.Option, error) {
	return ServeBridgeOptionsFor(r, svc, false)
}

// ServeBridgeOptionsFor is [ServeBridgeOptions] for a service whose
// exposure is known. loopback is true for a service reachable only
// from this machine — bound to a loopback address, a Unix socket, or
// stdio — which turns the rate limit's default off.
func ServeBridgeOptionsFor(r *Root, svc string, loopback bool) ([]cmdsurface.Option, error) {
	shared, err := r.serveBridgeOptions(svc)
	if err == nil {
		var limit []cmdsurface.Option
		limit, err = r.serveRateLimitOptions(svc, loopback)
		shared = append(limit, shared...)
	}
	if err != nil {
		return []cmdsurface.Option{cmdsurface.WithPermission(refuseAll(err))}, err
	}
	opts := append(r.serveRunnerOptions(), r.serveObservabilityOptions(svc)...)
	return append(opts, shared...), nil
}

// ValidateServeBridge is the configuration check the kit-shipped
// transport service svc runs in its Validate hook: a --policy that
// cannot load, an audit.redact block, audit.sinks list, rate_limit or
// concurrency block [ServeBridgeOptions] would refuse, an audit chain
// that cannot open, a timeouts block that does not parse or a
// kit/timeout annotation that does not, or a root factory that cannot
// build a usable tree, is a usage error before anything binds. The
// chains it opens are the ones Start reuses.
func ValidateServeBridge(r *Root, svc string) error {
	if _, err := r.servePermission(); err != nil {
		return err
	}
	if err := validateServeAudit(r, svc); err != nil {
		return err
	}
	if _, _, err := serveRateLimit(r.Viper, svc, false); err != nil {
		return err
	}
	if _, _, err := serveConcurrency(r.Viper, svc); err != nil {
		return err
	}
	if err := validateServeTimeouts(r, svc); err != nil {
		return err
	}
	if _, err := r.serveConfiguredAuditSinks(svc); err != nil {
		return err
	}
	if _, err := serveIdempotency(r.Viper, svc); err != nil {
		return err
	}
	return r.validateRootFactory()
}

// ServePolicyConfigured reports whether a delegation policy (--policy)
// is in force for this run — whether the permission gate the transport
// services share can refuse anything at all. Exposure checks use it to
// refuse an unbounded surface beyond loopback.
func ServePolicyConfigured(r *Root) bool { return r.servePolicyConfigured() }

// IsLoopbackAddr reports whether a host:port listen address binds a
// loopback interface only: 127.0.0.0/8, ::1, or the name localhost.
// An empty host binds every interface and is not loopback.
func IsLoopbackAddr(addr string) bool { return isLoopbackAddr(addr) }
