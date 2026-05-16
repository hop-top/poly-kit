package cli

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// withAnnotations is a helper that returns a child-builder mutator
// applying the given kit/* annotations to the constructed cobra
// command. Reads more linearly than building maps inline.
func withAnnotations(annos map[string]string) func(*cobra.Command) {
	return func(c *cobra.Command) {
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		for k, v := range annos {
			c.Annotations[k] = v
		}
	}
}

// --- Tier mapping: legacy + expanded ------------------------------

func TestWalkCobra_TierMappingTable(t *testing.T) {
	// Each row exercises one accepted side-effect annotation value
	// (legacy or expanded) and asserts the projected Safety.Level
	// plus the kit:fs:* permission token. Read column verifies
	// requiresConfirmation; the heuristic case is covered separately.
	cases := []struct {
		name        string
		sideEffect  string
		wantLevel   toolspec.SafetyLevel
		wantFSPerm  string
		wantConfirm bool
	}{
		// Legacy 4-tier
		{"legacy_read", "read", toolspec.SafetyLevelSafe, "kit:fs:read", false},
		{"legacy_write", "write", toolspec.SafetyLevelCaution, "kit:fs:write:shared", false},
		{"legacy_destructive", "destructive", toolspec.SafetyLevelDangerous, "kit:fs:destructive:shared", true},
		{"legacy_interactive", "interactive", toolspec.SafetyLevelCaution, "kit:fs:read", false},
		// Expanded 6-tier
		{"new_read", "read", toolspec.SafetyLevelSafe, "kit:fs:read", false},
		{"new_write_local", "write-local", toolspec.SafetyLevelCaution, "kit:fs:write:local", false},
		{"new_write_shared", "write-shared", toolspec.SafetyLevelCaution, "kit:fs:write:shared", false},
		{"new_destructive_local", "destructive-local", toolspec.SafetyLevelDangerous, "kit:fs:destructive:local", true},
		{"new_destructive_shared", "destructive-shared", toolspec.SafetyLevelDangerous, "kit:fs:destructive:shared", true},
		{"new_interactive", "interactive", toolspec.SafetyLevelCaution, "kit:fs:read", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := rootWith("mytool", addChild("op", withAnnotations(map[string]string{
				"kit/side-effect": tc.sideEffect,
			})))
			spec := WalkCobra(root)
			cmd := findCommand(spec.Commands, "op")
			require.NotNil(t, cmd)
			require.NotNil(t, cmd.Safety)
			assert.Equal(t, tc.wantLevel, cmd.Safety.Level)
			assert.Equal(t, tc.wantConfirm, cmd.Safety.RequiresConfirmation)

			require.NotEmpty(t, cmd.Safety.Permissions)
			assert.Equal(t, tc.wantFSPerm, cmd.Safety.Permissions[0],
				"first permission must be the fs token")
			// Default network permission lands second when absent.
			assert.Contains(t, cmd.Safety.Permissions, "kit:network:none")
		})
	}
}

// TestWalkCobra_HeuristicFallback verifies the destructive-name
// heuristic still escalates when no kit/side-effect annotation is
// declared. Since the heuristic doesn't know scope, it lands at
// destructive-shared per the conservative legacy mapping.
func TestWalkCobra_HeuristicFallback(t *testing.T) {
	for _, name := range []string{"delete", "remove", "rm", "destroy", "purge", "drop"} {
		t.Run(name, func(t *testing.T) {
			root := rootWith("mytool", addChild(name))
			spec := WalkCobra(root)
			cmd := findCommand(spec.Commands, name)
			require.NotNil(t, cmd)
			require.NotNil(t, cmd.Safety)
			assert.Equal(t, toolspec.SafetyLevelDangerous, cmd.Safety.Level)
			assert.True(t, cmd.Safety.RequiresConfirmation,
				"heuristic must keep requiring confirmation")
			assert.Equal(t, "kit:fs:destructive:shared", cmd.Safety.Permissions[0])
		})
	}
}

// TestWalkCobra_HeuristicYieldsToAnnotation verifies that an
// explicit kit/side-effect annotation overrides the destructive-name
// heuristic. A command literally called "delete" but tagged
// destructive-local must project the local permission, not shared.
func TestWalkCobra_HeuristicYieldsToAnnotation(t *testing.T) {
	root := rootWith("mytool", addChild("delete", withAnnotations(map[string]string{
		"kit/side-effect": "destructive-local",
	})))
	spec := WalkCobra(root)
	cmd := findCommand(spec.Commands, "delete")
	require.NotNil(t, cmd)
	require.NotNil(t, cmd.Safety)
	assert.Equal(t, "kit:fs:destructive:local", cmd.Safety.Permissions[0])
}

// --- Network mapping ----------------------------------------------

func TestWalkCobra_NetworkMappingTable(t *testing.T) {
	cases := []struct {
		name    string
		network string
		want    string
		confirm bool
	}{
		{"none_default_when_absent", "", "kit:network:none", false},
		{"explicit_none", "none", "kit:network:none", false},
		{"egress_public", "egress:public", "kit:network:egress:public", false},
		{"egress_private", "egress:private", "kit:network:egress:private", true},
		{"ingress", "ingress", "kit:network:ingress", true},
		{"unknown_falls_back_to_none", "garbage", "kit:network:none", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			annos := map[string]string{"kit/side-effect": "read"}
			if tc.network != "" {
				annos["kit/network"] = tc.network
			}
			root := rootWith("mytool", addChild("op", withAnnotations(annos)))
			spec := WalkCobra(root)
			cmd := findCommand(spec.Commands, "op")
			require.NotNil(t, cmd)
			require.NotNil(t, cmd.Safety)

			// Permissions slice always carries the network token at
			// position 1 (fs at position 0).
			require.GreaterOrEqual(t, len(cmd.Safety.Permissions), 2)
			assert.Equal(t, tc.want, cmd.Safety.Permissions[1])

			// Private egress / ingress escalate confirmation per
			// ADR-0019 default policy.
			assert.Equal(t, tc.confirm, cmd.Safety.RequiresConfirmation,
				"network %q should set RequiresConfirmation=%v", tc.network, tc.confirm)
		})
	}
}

// --- Capability annotations ---------------------------------------

func TestWalkCobra_CapabilityPermissions(t *testing.T) {
	t.Run("kit/exec adds subprocess permission", func(t *testing.T) {
		root := rootWith("mytool", addChild("build", withAnnotations(map[string]string{
			"kit/side-effect": "write-local",
			"kit/exec":        "true",
		})))
		spec := WalkCobra(root)
		cmd := findCommand(spec.Commands, "build")
		require.NotNil(t, cmd)
		require.NotNil(t, cmd.Safety)
		assert.Contains(t, cmd.Safety.Permissions, "kit:exec:subprocess")
	})

	t.Run("kit/bus-publish adds bus permission", func(t *testing.T) {
		root := rootWith("mytool", addChild("emit", withAnnotations(map[string]string{
			"kit/side-effect": "write-local",
			"kit/bus-publish": "true",
		})))
		spec := WalkCobra(root)
		cmd := findCommand(spec.Commands, "emit")
		require.NotNil(t, cmd)
		require.NotNil(t, cmd.Safety)
		assert.Contains(t, cmd.Safety.Permissions, "kit:bus:publish")
	})

	t.Run("absent capabilities not emitted", func(t *testing.T) {
		root := rootWith("mytool", addChild("plain", withAnnotations(map[string]string{
			"kit/side-effect": "read",
		})))
		spec := WalkCobra(root)
		cmd := findCommand(spec.Commands, "plain")
		require.NotNil(t, cmd)
		require.NotNil(t, cmd.Safety)
		assert.NotContains(t, cmd.Safety.Permissions, "kit:exec:subprocess")
		assert.NotContains(t, cmd.Safety.Permissions, "kit:bus:publish")
	})
}

// --- Manifest JSON round-trip -------------------------------------

// TestWalkCobra_PermissionsJSONRoundTrip verifies the projected
// Safety.Permissions slice survives JSON marshal/unmarshal — the
// shape contract for adopters consuming the toolspec wire form.
func TestWalkCobra_PermissionsJSONRoundTrip(t *testing.T) {
	root := rootWith("mytool", addChild("publish", withAnnotations(map[string]string{
		"kit/side-effect": "destructive-shared",
		"kit/network":     "egress:public",
		"kit/exec":        "true",
		"kit/bus-publish": "true",
	})))

	spec := WalkCobra(root)

	data, err := json.Marshal(spec)
	require.NoError(t, err)

	var decoded toolspec.ToolSpec
	require.NoError(t, json.Unmarshal(data, &decoded))

	pub := findCommand(decoded.Commands, "publish")
	require.NotNil(t, pub)
	require.NotNil(t, pub.Safety)
	assert.Equal(t, toolspec.SafetyLevelDangerous, pub.Safety.Level)
	assert.True(t, pub.Safety.RequiresConfirmation)
	assert.ElementsMatch(t,
		[]string{
			"kit:fs:destructive:shared",
			"kit:network:egress:public",
			"kit:exec:subprocess",
			"kit:bus:publish",
		},
		pub.Safety.Permissions,
	)
}

// TestWalkCobra_LegacyAnnotationJSONRoundTrip confirms a legacy
// "write" annotation projects through to write-shared in JSON.
func TestWalkCobra_LegacyAnnotationJSONRoundTrip(t *testing.T) {
	root := rootWith("mytool", addChild("update", withAnnotations(map[string]string{
		"kit/side-effect": "write",
	})))
	spec := WalkCobra(root)
	data, err := json.Marshal(spec)
	require.NoError(t, err)
	var decoded toolspec.ToolSpec
	require.NoError(t, json.Unmarshal(data, &decoded))
	upd := findCommand(decoded.Commands, "update")
	require.NotNil(t, upd)
	require.NotNil(t, upd.Safety)
	assert.Equal(t, toolspec.SafetyLevelCaution, upd.Safety.Level)
	assert.Equal(t, "kit:fs:write:shared", upd.Safety.Permissions[0])
}

// TestWalkCobra_DefaultPermissionsForUnannotatedReadCommand pins the
// shape produced for a typical unannotated leaf — no destructive
// name, no kit/side-effect — to guard against accidental escalation
// in the default-safety fallback.
func TestWalkCobra_DefaultPermissionsForUnannotatedReadCommand(t *testing.T) {
	root := rootWith("mytool", addChild("list"))
	spec := WalkCobra(root)
	cmd := findCommand(spec.Commands, "list")
	require.NotNil(t, cmd)
	require.NotNil(t, cmd.Safety)
	assert.Equal(t, toolspec.SafetyLevelSafe, cmd.Safety.Level)
	assert.False(t, cmd.Safety.RequiresConfirmation)
	assert.Equal(t,
		[]string{"kit:fs:read", "kit:network:none"},
		cmd.Safety.Permissions,
	)
}
