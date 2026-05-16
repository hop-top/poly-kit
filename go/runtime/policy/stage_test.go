package policy_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

// TestStageYAML_Loads is a smoke test: stage.yaml ships valid policies
// (every rule parses, names unique, all topics on the allowed list).
func TestStageYAML_Loads(t *testing.T) {
	cfg, err := policy.LoadConfig(filepath.Join("stage.yaml"))
	require.NoError(t, err, "stage.yaml must parse cleanly")
	require.Len(t, cfg.Policies, 6, "expected 6 default rules")

	want := []string{
		"archived-blocks-all-mutations",
		"sunset-blocks-creates",
		"feature-freeze-blocks-track-create",
		"feature-freeze-blocks-feature-tasks",
		"maintenance-blocks-track-create",
		"public-feedback-allows-feedback-only-tracks",
	}
	for i, name := range want {
		assert.Equal(t, name, cfg.Policies[i].Name, "rule %d", i)
	}
}

// matrixCase encodes one stage × {entity.kind, op, track_type} cell of
// the 6-stage acceptance matrix. AllowedExpected = true means the
// stage.yaml ruleset MUST allow this op; false means it MUST deny.
type matrixCase struct {
	Stage          string
	Kind           string
	Op             string
	TrackType      string
	AllowExpected  bool
	DenyRuleSubstr string // when AllowExpected==false, the denying rule's name should contain this
}

// TestStageYAML_AllowDenyMatrix exhaustively exercises stage.yaml
// against the 6 stages × {feature track create, fix task create, doc
// task create, update, delete}. Mirrors the acceptance matrix in the
// track plan.
func TestStageYAML_AllowDenyMatrix(t *testing.T) {
	cfg, err := policy.LoadConfig("stage.yaml")
	require.NoError(t, err)

	cases := []matrixCase{
		// active: everything allowed.
		{Stage: "active", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: true},
		{Stage: "active", Kind: "task", Op: "create", TrackType: "fix", AllowExpected: true},
		{Stage: "active", Kind: "task", Op: "create", TrackType: "docs", AllowExpected: true},
		{Stage: "active", Kind: "task", Op: "delete", TrackType: "feature", AllowExpected: true},

		// public_feedback: only feedback tracks may be created;
		// non-feedback track-create is denied; tasks always pass.
		{Stage: "public_feedback", Kind: "track", Op: "create", TrackType: "feedback", AllowExpected: true},
		{Stage: "public_feedback", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "public-feedback"},
		{Stage: "public_feedback", Kind: "track", Op: "create", TrackType: "fix", AllowExpected: false, DenyRuleSubstr: "public-feedback"},
		{Stage: "public_feedback", Kind: "task", Op: "create", TrackType: "feature", AllowExpected: true},
		{Stage: "public_feedback", Kind: "track", Op: "update", TrackType: "feature", AllowExpected: true},

		// feature_freeze: tracks blocked; feature/refactor tasks
		// blocked; fix/chore/docs tasks allowed.
		{Stage: "feature_freeze", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "feature-freeze-blocks-track-create"},
		{Stage: "feature_freeze", Kind: "task", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "feature-freeze-blocks-feature-tasks"},
		{Stage: "feature_freeze", Kind: "task", Op: "create", TrackType: "refactor", AllowExpected: false, DenyRuleSubstr: "feature-freeze-blocks-feature-tasks"},
		{Stage: "feature_freeze", Kind: "task", Op: "create", TrackType: "fix", AllowExpected: true},
		{Stage: "feature_freeze", Kind: "task", Op: "create", TrackType: "chore", AllowExpected: true},
		{Stage: "feature_freeze", Kind: "task", Op: "create", TrackType: "docs", AllowExpected: true},

		// maintenance: tracks blocked; tasks (any type) allowed.
		{Stage: "maintenance", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "maintenance-blocks-track-create"},
		{Stage: "maintenance", Kind: "task", Op: "create", TrackType: "fix", AllowExpected: true},
		{Stage: "maintenance", Kind: "task", Op: "create", TrackType: "docs", AllowExpected: true},
		{Stage: "maintenance", Kind: "task", Op: "update", TrackType: "fix", AllowExpected: true},

		// sunset: creates blocked; updates/deletes allowed.
		{Stage: "sunset", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "sunset-blocks-creates"},
		{Stage: "sunset", Kind: "task", Op: "create", TrackType: "fix", AllowExpected: false, DenyRuleSubstr: "sunset-blocks-creates"},
		{Stage: "sunset", Kind: "track", Op: "update", TrackType: "feature", AllowExpected: true},
		{Stage: "sunset", Kind: "task", Op: "delete", TrackType: "fix", AllowExpected: true},

		// archived: all mutations blocked.
		{Stage: "archived", Kind: "track", Op: "create", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "archived-blocks"},
		{Stage: "archived", Kind: "track", Op: "update", TrackType: "feature", AllowExpected: false, DenyRuleSubstr: "archived-blocks"},
		{Stage: "archived", Kind: "task", Op: "delete", TrackType: "fix", AllowExpected: false, DenyRuleSubstr: "archived-blocks"},
	}

	for _, c := range cases {
		c := c
		name := c.Stage + "_" + c.Kind + "_" + c.Op + "_" + c.TrackType
		t.Run(name, func(t *testing.T) {
			eng, err := withcel.New(cfg,
				policy.WithStageResolver(func(_ string) (map[string]any, error) {
					return map[string]any{"mode": c.Stage}, nil
				}),
			)
			require.NoError(t, err)

			activation := map[string]any{
				"principal": map[string]any{"id": "alice", "role": "user"},
				"resource":  map[string]any{"id": "ops", "kind": c.Kind},
				"entity": map[string]any{
					"kind":       c.Kind,
					"op":         c.Op,
					"track_type": c.TrackType,
				},
				"context": map[string]any{},
				"payload": map[string]any{"scope": "ops"},
			}
			err = eng.Decide("kit.runtime.entity.pre_validated", activation)

			if c.AllowExpected {
				assert.NoError(t, err, "expected allow for %s", name)
				return
			}
			require.Error(t, err, "expected deny for %s", name)
			var pde *policy.PolicyDeniedError
			require.ErrorAs(t, err, &pde)
			assert.Contains(t, pde.PolicyName, c.DenyRuleSubstr,
				"deny rule should match expected name substring")
		})
	}
}
