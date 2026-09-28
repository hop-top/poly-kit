package celpermission_test

import (
	"context"
	"testing"

	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface"
)

// budgetRules is a realistic permissions: block: a tenant boundary, a
// scope-and-flag rule, and a network rule on destructive tiers.
var budgetRules = []policy.PermissionRule{
	deny("tenant-boundary", `principal.tenant != "acme"`, "acme only"),
	deny("force-needs-admin", `has(payload.flags.force) && payload.flags.force && !("items:admin" in principal.scopes)`, "force needs items:admin"),
	deny("destructive-from-office", `resource.tier.startsWith("destructive") && !context.client_addr.startsWith("10.")`, "destructive from the office network only"),
}

// benchGate returns the compiled gate, the leaf it is asked about, and
// the context carrying an admitted invocation.
func benchGate(tb testing.TB) (cmdsurface.PermissionFunc, *cmdsurface.Leaf, context.Context, cmdsurface.Meta) {
	tb.Helper()
	gate, err := celpermission.New(budgetRules)
	if err != nil {
		tb.Fatal(err)
	}
	var leaf *cmdsurface.Leaf
	var ctx context.Context
	inv := cmdsurface.Invocation{
		Path:  []string{"item", "add"},
		Args:  []string{"washer"},
		Flags: map[string]any{"force": true, "count": 3},
		Meta:  verified("alice", "acme", "items:read,items:admin"),
	}
	// Capture the context the bridge hands the gate, so the benchmark
	// measures the rules on exactly what they see in production.
	b := cmdsurface.New(tree(), cmdsurface.WithRunner(okRunner{}), cmdsurface.WithPermission(
		func(c context.Context, _ cmdsurface.Meta, l *cmdsurface.Leaf) cmdsurface.PermissionDecision {
			ctx, leaf = c, l
			return cmdsurface.PermissionDecision{Allowed: true}
		}))
	b.Expose("*", cmdsurface.SurfaceREST)
	if _, err := b.Invoke(context.Background(), inv); err != nil {
		tb.Fatal(err)
	}
	if dec := gate(ctx, inv.Meta, leaf); !dec.Allowed {
		tb.Fatalf("benchmark invocation refused: %s", dec.Reason)
	}
	return gate, leaf, ctx, inv.Meta
}

// BenchmarkGate measures one permission decision over budgetRules: the
// cost the rules add to every served invocation.
func BenchmarkGate(b *testing.B) {
	gate, leaf, ctx, meta := benchGate(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if dec := gate(ctx, meta, leaf); !dec.Allowed {
			b.Fatal(dec.Reason)
		}
	}
}

// budgetPerDecision is the documented ceiling for one decision over
// budgetRules. Measured cost is far below it on a laptop; the ceiling
// leaves room for a loaded CI runner and exists to catch a structural
// regression, such as compiling per call, which costs orders of
// magnitude more.
const budgetPerDecision = 50_000 // ns

// TestGate_WithinBudget holds a decision under budgetPerDecision.
func TestGate_WithinBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing: skipped under -short and -race")
	}
	res := testing.Benchmark(BenchmarkGate)
	if res.N == 0 {
		t.Fatal("benchmark did not run")
	}
	if ns := res.NsPerOp(); ns > budgetPerDecision {
		t.Fatalf("one decision over %d rules costs %d ns, budget %d ns", len(budgetRules), ns, budgetPerDecision)
	}
}
