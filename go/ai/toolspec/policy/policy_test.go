package policy_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/policy"
)

// TestDefault_LoadsAndParses asserts the embedded default.yaml
// parses cleanly and surfaces every documented (side_effect,
// network) cell from ADR-0019 §4.
func TestDefault_LoadsAndParses(t *testing.T) {
	t.Parallel()
	tbl := policy.Default()
	assert.Equal(t, "1.0", tbl.SchemaVersion)
	assert.NotEmpty(t, tbl.Rules)
}

// TestDefault_EveryCellHasDecision walks every (side_effect, network)
// tuple in the documented matrix and asserts the table has an
// explicit decision for it. ADR-0019 promises every cell is
// documented; this test makes that contract testable.
func TestDefault_EveryCellHasDecision(t *testing.T) {
	t.Parallel()
	tbl := policy.Default()

	sideEffects := []policy.SideEffect{
		policy.SideEffectRead,
		policy.SideEffectWrite,
		policy.SideEffectDestructive,
		policy.SideEffectInteractive,
	}
	networks := []policy.Network{
		policy.NetworkNone,
		policy.NetworkLocalOnly,
		policy.NetworkEgress,
	}

	for _, se := range sideEffects {
		for _, net := range networks {
			d := tbl.Resolve(se, net)
			assert.NotEqualf(t, "fallback", d.Source,
				"(%s, %s) should hit a documented rule, not fallback", se, net)
			assert.NotEmptyf(t, d.Reason,
				"(%s, %s) decision should carry a reason", se, net)
			switch d.Action {
			case policy.ActionAutoAllow, policy.ActionPrompt, policy.ActionDeny:
				// valid
			default:
				t.Fatalf("(%s, %s) → invalid action %q", se, net, d.Action)
			}
		}
	}
}

// TestDefault_ExpectedDecisions locks the headline cells from
// ADR-0019 §4 — the table consumers reason about. Drift here is
// deliberate: bumping the doc requires updating this test.
func TestDefault_ExpectedDecisions(t *testing.T) {
	t.Parallel()
	tbl := policy.Default()

	cases := []struct {
		se   policy.SideEffect
		net  policy.Network
		want policy.Action
	}{
		{policy.SideEffectRead, policy.NetworkNone, policy.ActionAutoAllow},
		{policy.SideEffectRead, policy.NetworkLocalOnly, policy.ActionAutoAllow},
		{policy.SideEffectRead, policy.NetworkEgress, policy.ActionPrompt},

		{policy.SideEffectWrite, policy.NetworkNone, policy.ActionAutoAllow},
		{policy.SideEffectWrite, policy.NetworkLocalOnly, policy.ActionPrompt},
		{policy.SideEffectWrite, policy.NetworkEgress, policy.ActionPrompt},

		{policy.SideEffectDestructive, policy.NetworkNone, policy.ActionPrompt},
		{policy.SideEffectDestructive, policy.NetworkLocalOnly, policy.ActionPrompt},
		{policy.SideEffectDestructive, policy.NetworkEgress, policy.ActionDeny},

		{policy.SideEffectInteractive, policy.NetworkNone, policy.ActionPrompt},
		{policy.SideEffectInteractive, policy.NetworkLocalOnly, policy.ActionPrompt},
		{policy.SideEffectInteractive, policy.NetworkEgress, policy.ActionPrompt},
	}
	for _, tc := range cases {
		t.Run(string(tc.se)+"/"+string(tc.net), func(t *testing.T) {
			d := tbl.Resolve(tc.se, tc.net)
			assert.Equal(t, tc.want, d.Action)
		})
	}
}

func TestResolve_FallbackPrompt(t *testing.T) {
	t.Parallel()
	tbl := policy.Default()
	d := tbl.Resolve("frobnicate", "intergalactic")
	assert.Equal(t, policy.ActionPrompt, d.Action,
		"unknown tuple → fail-safe prompt")
	assert.Equal(t, "fallback", d.Source)
	assert.Contains(t, d.Reason, "no rule matched")
}

func TestResolve_NetworkAnyWildcard(t *testing.T) {
	t.Parallel()
	tbl := policy.Default()
	// interactive uses network: any in default.yaml.
	for _, net := range []policy.Network{
		policy.NetworkNone, policy.NetworkLocalOnly, policy.NetworkEgress,
	} {
		d := tbl.Resolve(policy.SideEffectInteractive, net)
		assert.Equal(t, policy.ActionPrompt, d.Action,
			"interactive should prompt across all network values")
		assert.NotEqual(t, "fallback", d.Source,
			"interactive any-rule must catch network=%q", net)
	}
}

func TestLoadBytes_ParsesCustomTable(t *testing.T) {
	t.Parallel()
	custom := []byte(`
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: prompt
    reason: "tighter rule for production"
`)
	tbl, err := policy.LoadBytes(custom, "test.yaml")
	require.NoError(t, err)
	require.Len(t, tbl.Rules, 1)
	assert.Equal(t, "test.yaml", tbl.Rules[0].Source)
	assert.Equal(t, policy.ActionPrompt, tbl.Rules[0].Action)
}

func TestLoadBytes_RejectsBadYAML(t *testing.T) {
	t.Parallel()
	_, err := policy.LoadBytes([]byte("not: [valid"), "bad.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad.yaml")
}

func TestMerge_OverlayWins(t *testing.T) {
	t.Parallel()
	base := policy.Default()
	overlay, err := policy.LoadBytes([]byte(`
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: prompt
    reason: "production override"
`), "production.yaml")
	require.NoError(t, err)

	merged := policy.Merge(base, overlay)
	d := merged.Resolve(policy.SideEffectRead, policy.NetworkNone)
	assert.Equal(t, policy.ActionPrompt, d.Action,
		"overlay rule wins over default's auto-allow")
	assert.Equal(t, "production.yaml", d.Source)
	assert.Contains(t, d.Reason, "production override")
}

func TestMerge_FallsThroughToBase(t *testing.T) {
	t.Parallel()
	base := policy.Default()
	overlay := policy.Table{
		SchemaVersion: "1.0",
		Rules: []policy.Rule{{
			SideEffect: policy.SideEffectRead,
			Network:    policy.NetworkNone,
			Action:     policy.ActionPrompt,
			Reason:     "tightened",
		}},
	}
	merged := policy.Merge(base, overlay)
	// The non-overridden cells must keep their base behavior.
	d := merged.Resolve(policy.SideEffectDestructive, policy.NetworkEgress)
	assert.Equal(t, policy.ActionDeny, d.Action,
		"base rules survive the merge for cells the overlay doesn't touch")
}

func TestMerge_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()
	base := policy.Default()
	baseSnapshot := strings.Builder{}
	for _, r := range base.Rules {
		baseSnapshot.WriteString(string(r.SideEffect))
		baseSnapshot.WriteString("/")
		baseSnapshot.WriteString(string(r.Network))
		baseSnapshot.WriteString(",")
	}
	overlay := policy.Table{
		Rules: []policy.Rule{{
			SideEffect: policy.SideEffectRead,
			Network:    policy.NetworkNone,
			Action:     policy.ActionPrompt,
			Reason:     "x",
		}},
	}
	_ = policy.Merge(base, overlay)
	current := strings.Builder{}
	for _, r := range base.Rules {
		current.WriteString(string(r.SideEffect))
		current.WriteString("/")
		current.WriteString(string(r.Network))
		current.WriteString(",")
	}
	assert.Equal(t, baseSnapshot.String(), current.String(),
		"Merge must not mutate base")
}
