package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/cmdsurface"
)

// TestPermissionBudgetChargedOnlyOnceSlotSixAdmits pins that a caller
// rule's max_ops budget is charged after every slot-6 decider admits
// the call: a refusal by the permission rules or by the adopter's gate
// spends nothing, a probe spends nothing, an admitted call spends one.
func TestPermissionBudgetChargedOnlyOnceSlotSixAdmits(t *testing.T) {
	p := callersPolicy(5)
	p.Permissions = []policy.PermissionRule{{Name: "stub", When: "true", Effect: policy.RuleAllow}}
	compile := func([]policy.PermissionRule) (cmdsurface.PermissionFunc, error) {
		return func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
			if meta.Extra["deny"] == "rules" {
				return cmdsurface.PermissionDecision{Reason: `permission rule "stub": no`}
			}
			return cmdsurface.PermissionDecision{Allowed: true}
		}, nil
	}
	adopter := func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if meta.Extra["deny"] == "adopter" {
			return cmdsurface.PermissionDecision{Reason: "adopter: no"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	r := authRoot(t, WithPermissionRules(compile), WithPermission(adopter), WithUsageStore(memory.New()))
	setPolicy(t, r, p)
	gate, err := r.servePermission(ServeExposure{Loopback: true})
	require.NoError(t, err)

	ledger, err := usageLedgerOf(r)
	require.NoError(t, err)
	budget, ok := policy.NewEngine(p, 0).BudgetFor(&policy.Caller{Principal: "alice", Tenant: "acme"})
	require.True(t, ok)
	spent := func() int64 {
		u, err := ledger.Usage(context.Background(), budget.Key, budget.Window)
		require.NoError(t, err)
		return u.Ops
	}
	alice := func(deny string) cmdsurface.Meta {
		return cmdsurface.Meta{Caller: "alice", Tenant: "acme", Established: cmdsurface.EstablishedVerified,
			Extra: map[string]string{"deny": deny}}
	}
	add := leafOf(t, r, "add")
	ctx := context.Background()

	dec := gate(ctx, alice("rules"), add)
	require.False(t, dec.Allowed)
	assert.Equal(t, `permission rule "stub": no`, dec.Reason)
	assert.Zero(t, spent(), "a call the rules refuse spends no budget")

	dec = gate(ctx, alice("adopter"), add)
	require.False(t, dec.Allowed)
	assert.Equal(t, "adopter: no", dec.Reason)
	assert.Zero(t, spent(), "a call the adopter refuses spends no budget")

	require.True(t, gate(cmdsurface.ProbeContext(ctx), alice(""), add).Allowed)
	assert.Zero(t, spent(), "a probe spends no budget")

	require.True(t, gate(ctx, alice(""), add).Allowed)
	assert.Equal(t, int64(1), spent(), "an admitted call spends one")
}

// TestPermissionBudgetSpentRefusesBeforeLaterDeciders pins that a spent
// budget still refuses at the policy's turn: the rules and the adopter
// are not asked about a call the budget refuses.
func TestPermissionBudgetSpentRefusesBeforeLaterDeciders(t *testing.T) {
	var adopterAsked int
	adopter := func(context.Context, cmdsurface.Meta, *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		adopterAsked++
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	r := authRoot(t, WithPermission(adopter), WithUsageStore(memory.New()))
	setPolicy(t, r, callersPolicy(1))
	gate, err := r.servePermission(ServeExposure{Loopback: true})
	require.NoError(t, err)

	alice := cmdsurface.Meta{Caller: "alice", Tenant: "acme", Established: cmdsurface.EstablishedVerified}
	add := leafOf(t, r, "add")
	require.True(t, gate(context.Background(), alice, add).Allowed)
	require.Equal(t, 1, adopterAsked)

	dec := gate(context.Background(), alice, add)
	require.False(t, dec.Allowed)
	assert.Contains(t, dec.Reason, "max_ops budget of 1 per 1h0m0s spent")
	assert.Equal(t, 1, adopterAsked, "the adopter is not asked about a call the budget refused")
}
