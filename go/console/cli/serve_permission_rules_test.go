package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// rulesPolicy refuses the write class and declares one permission
// rule; the stub compiler below stands in for the CEL evaluator.
var rulesPolicy = policy.Policy{
	Name:  "rules",
	Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}},
	Permissions: []policy.PermissionRule{{
		Name: "no-mallory", When: `principal.id == "mallory"`,
		Effect: policy.RuleDeny, Otherwise: policy.RuleAllow, Message: "mallory is out",
	}},
}

// leafOf returns the Leaf the bridge would hand the gate for use.
func leafOf(t *testing.T, r *Root, use string) *cmdsurface.Leaf {
	t.Helper()
	for _, c := range r.Cmd.Commands() {
		if c.Name() == use {
			return &cmdsurface.Leaf{Path: []string{use}, Cmd: c}
		}
	}
	t.Fatalf("no %q command", use)
	return nil
}

// setPolicy names p on r's --policy flag, the way an operator does.
func setPolicy(t *testing.T, r *Root, p policy.Policy) {
	t.Helper()
	WithPolicy(func(string) (policy.Policy, error) { return p, nil })(r)
	require.NoError(t, r.Cmd.PersistentFlags().Set(policyFlag, p.Name))
}

// TestPermissionRulesSitBetweenThePolicyAndTheAdopter pins slot 6's
// order with rules wired: the policy's allow lists refuse first, the
// rules next, the adopter's gate last, and a decider is never asked
// about a call an earlier one refused.
func TestPermissionRulesSitBetweenThePolicyAndTheAdopter(t *testing.T) {
	var compiled []policy.PermissionRule
	var rulesAsked, adopterAsked []string
	compile := func(rules []policy.PermissionRule) (cmdsurface.PermissionFunc, error) {
		compiled = rules
		return func(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
			rulesAsked = append(rulesAsked, leaf.PathKey())
			if meta.Caller == "mallory" {
				return cmdsurface.PermissionDecision{Reason: `permission rule "no-mallory": mallory is out`}
			}
			return cmdsurface.PermissionDecision{Allowed: true}
		}, nil
	}
	adopter := func(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		adopterAsked = append(adopterAsked, leaf.PathKey())
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	r := authRoot(t, WithPermissionRules(compile), WithPermission(adopter))
	setPolicy(t, r, rulesPolicy)

	gate, err := r.servePermission()
	require.NoError(t, err)
	assert.Equal(t, rulesPolicy.Permissions, compiled, "the compiler receives the policy's permissions: block")

	ctx := context.Background()
	dec := gate(ctx, cmdsurface.Meta{Caller: "alice"}, leafOf(t, r, "add"))
	assert.False(t, dec.Allowed, "the policy refuses the write class")
	assert.True(t, dec.CallerIndependent)
	assert.Empty(t, rulesAsked, "rules are not asked about a call the policy refused")

	dec = gate(ctx, cmdsurface.Meta{Caller: "mallory"}, leafOf(t, r, "list"))
	assert.False(t, dec.Allowed)
	assert.Equal(t, `permission rule "no-mallory": mallory is out`, dec.Reason)
	assert.Empty(t, adopterAsked, "the adopter is not asked about a call the rules refused")

	dec = gate(ctx, cmdsurface.Meta{Caller: "alice"}, leafOf(t, r, "list"))
	assert.True(t, dec.Allowed)
	assert.Equal(t, []string{"list", "list"}, rulesAsked)
	assert.Equal(t, []string{"list"}, adopterAsked)
}

// TestPermissionRulesWithoutAnEvaluatorRefuseToServe pins fail-closed
// wiring: a policy that declares rules, served by a tool that wires no
// evaluator, is a usage error, not a gate without its rules.
func TestPermissionRulesWithoutAnEvaluatorRefuseToServe(t *testing.T) {
	r := authRoot(t)
	setPolicy(t, r, rulesPolicy)

	_, err := r.servePermission()
	require.Error(t, err)
	var ce *output.Error
	require.True(t, errors.As(err, &ce), "a usage error: %v", err)
	assert.Equal(t, output.ExitUsage, ce.ExitCode)
	assert.Contains(t, err.Error(), `policy "rules" declares permissions: rules, but this tool wires no rule evaluator`)

	opts, err := ServeBridgeOptions(r, APIServiceName)
	require.Error(t, err)
	require.NotEmpty(t, opts, "the refusing options still gate every call")
}

// TestPermissionRulesThatDoNotCompileRefuseToServe pins that the
// compiler's error, naming the rule, becomes the start refusal.
func TestPermissionRulesThatDoNotCompileRefuseToServe(t *testing.T) {
	compile := func([]policy.PermissionRule) (cmdsurface.PermissionFunc, error) {
		return nil, errors.New(`permission rule "no-mallory" does not compile: syntax error`)
	}
	r := authRoot(t, WithPermissionRules(compile))
	setPolicy(t, r, rulesPolicy)

	err := ValidateServeBridge(r, APIServiceName)
	require.Error(t, err)
	var ce *output.Error
	require.True(t, errors.As(err, &ce), "a usage error: %v", err)
	assert.Equal(t, output.ExitUsage, ce.ExitCode)
	assert.Contains(t, err.Error(), `policy "rules": permission rule "no-mallory" does not compile`)
}

// TestNoPermissionRulesCompilesNothing pins that a policy without a
// permissions: block never reaches the evaluator.
func TestNoPermissionRulesCompilesNothing(t *testing.T) {
	called := false
	compile := func([]policy.PermissionRule) (cmdsurface.PermissionFunc, error) {
		called = true
		return cmdsurface.PermitAll, nil
	}
	r := authRoot(t, WithPermissionRules(compile))
	setPolicy(t, r, policy.Policy{Name: "plain"})
	_, err := r.servePermission()
	require.NoError(t, err)
	assert.False(t, called)
}
