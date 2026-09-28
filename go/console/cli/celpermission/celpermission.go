// Package celpermission compiles the permissions: block of a tool's
// --policy file into a permission gate for served invocations, using
// the same CEL evaluator and rule semantics hop.top/kit/go/runtime/policy
// applies to bus events: one rule language for both.
//
// It lives apart from go/console/cli so a kit CLI that never serves, or
// serves without rules, does not link cel-go. Wire it once:
//
//	root := cli.New(cfg,
//	    cli.WithPolicy(cli.DefaultPolicyLoader("mytool")),
//	    cli.WithAPI(cli.APIConfig{Auth: authenticate}),
//	    celpermission.With(),
//	)
//
// and every kit-shipped service's permission gate evaluates the rules
// of the --policy it serves under, after the built-in scope check and
// the policy's allow lists. The rules can only narrow what those
// admitted.
//
// Each rule sees one invocation through the bindings runtime/policy
// declares:
//
//	principal.id           caller (Meta.Caller)
//	principal.tenant       tenant (Meta.Tenant)
//	principal.scopes       the verified credential's scopes; [] otherwise
//	principal.established  true when the transport established the caller
//	principal.source       "verified" | "transport" | "none"
//	resource.kind          "command"
//	resource.id            the command path, space-joined ("item add")
//	resource.path          the command path as a list
//	resource.tier          the side-effect tier ("read", "write-local", ...)
//	context.surface        the surface the call arrived on ("rest", "mcp", ...)
//	context.client_addr    the caller's IP as the transport recorded it; "" when none
//	payload.args           the positional arguments
//	payload.flags          the flags the caller set, by long name
//
// Across rules deny overrides; a rule whose expression fails to
// evaluate — a flag the caller did not set, read without has() —
// denies.
package celpermission

import (
	"context"
	"errors"
	"fmt"
	"net"

	"hop.top/kit/go/console/cli"
	clipolicy "hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/cel"
	"hop.top/kit/go/transport/cmdsurface"
)

// topic is the engine-internal topic every permission rule is filed
// under. It is never published: the engine needs a topic to select
// rules by, and a served invocation has no bus event of its own.
const topic = "kit.transport.invocation.permission_decided"

// With installs [New] as the tool's rule evaluator: the permissions:
// rules of the active --policy become part of the permission gate of
// every kit-shipped transport service. A policy whose rules do not
// compile refuses the service at start, naming the rule.
func With() func(*cli.Root) {
	return cli.WithPermissionRules(New)
}

// New compiles rules into a [cmdsurface.PermissionFunc]. Every rule is
// validated and compiled here, so a rule that cannot run fails
// construction with an error naming it rather than denying at the
// first call. No rules yields [cmdsurface.PermitAll].
//
// A refusal's reason is `permission rule "<name>": <message>`, stable
// for a given rule, and it is what the bridge puts in the
// [cmdsurface.ErrPermissionDenied] error and the audit record. A
// decision is never CallerIndependent: rules read the caller.
func New(rules []clipolicy.PermissionRule) (cmdsurface.PermissionFunc, error) {
	if err := clipolicy.ValidatePermissionRules(rules); err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return cmdsurface.PermitAll, nil
	}
	cfg := &policy.Config{Policies: make([]policy.Policy, 0, len(rules))}
	for _, r := range rules {
		cfg.Policies = append(cfg.Policies, policy.Policy{
			Name:      r.Name,
			On:        topic,
			When:      r.When,
			Effect:    policy.Effect(r.Effect),
			Otherwise: policy.Effect(r.Otherwise),
			Message:   r.Message,
		})
	}
	// Compiled one by one first, so a rule that does not compile is
	// named by the error; the engine then takes the same evaluator.
	ev, err := cel.New()
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
	}
	for _, r := range rules {
		if err := ev.Compile(r.Name, r.When); err != nil {
			if inner := errors.Unwrap(err); inner != nil {
				err = inner
			}
			return nil, fmt.Errorf("permission rule %q does not compile: %w", r.Name, err)
		}
	}
	eng, err := policy.NewEngine(cfg, policy.WithEvaluator(ev))
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
	}
	return func(ctx context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if leaf == nil {
			return cmdsurface.PermissionDecision{Allowed: true}
		}
		err := eng.Decide(topic, activation(ctx, meta, leaf))
		if err == nil {
			return cmdsurface.PermissionDecision{Allowed: true}
		}
		return cmdsurface.PermissionDecision{Reason: reason(err)}
	}, nil
}

// reason renders a denial as the gate's stable reason.
func reason(err error) string {
	var denied *policy.PolicyDeniedError
	if !errors.As(err, &denied) {
		return "permission rules: " + err.Error()
	}
	if denied.Message == "" {
		return fmt.Sprintf("permission rule %q", denied.PolicyName)
	}
	return fmt.Sprintf("permission rule %q: %s", denied.PolicyName, denied.Message)
}

// activation builds the bindings one rule evaluation sees. Every map
// and list is non-nil so has() and size() work on an empty value.
func activation(ctx context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) map[string]any {
	inv, _ := cmdsurface.InvocationFromContext(ctx)
	scopes := meta.VerifiedScopes()
	if scopes == nil {
		scopes = []string{}
	}
	args := inv.Args
	if args == nil {
		args = []string{}
	}
	flags := inv.Flags
	if flags == nil {
		flags = map[string]any{}
	}
	tier := ""
	if leaf.Descriptor != nil {
		tier = string(leaf.Descriptor.Safety.Tier)
	}
	return map[string]any{
		"principal": map[string]any{
			"id":          meta.Caller,
			"tenant":      meta.Tenant,
			"scopes":      scopes,
			"established": meta.Authenticated(),
			"source":      source(meta.Established),
		},
		"resource": map[string]any{
			"kind": "command",
			"id":   leaf.PathKey(),
			"path": leaf.Path,
			"tier": tier,
		},
		"context": map[string]any{
			"surface":     string(meta.Surface),
			"client_addr": clientAddr(meta.Extra["remote_addr"]),
		},
		"payload": map[string]any{
			"args":  args,
			"flags": flags,
		},
		"entity": map[string]any{},
	}
}

// source names how the caller was established, in the words rules
// compare against.
func source(e cmdsurface.Establishment) string {
	switch e {
	case cmdsurface.EstablishedVerified:
		return "verified"
	case cmdsurface.EstablishedTransport:
		return "transport"
	default:
		return "none"
	}
}

// clientAddr is the caller's address without its port: each connection
// has its own ephemeral port, which no rule can usefully name.
func clientAddr(addr string) string {
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
