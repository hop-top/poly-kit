package cli_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// TestPlan_RoundTrip_JSON locks the JSON shape: every field declared in
// cli.Plan / cli.Effect survives a marshal+unmarshal cycle unchanged.
func TestPlan_RoundTrip_JSON(t *testing.T) {
	now := time.Date(2026, 5, 2, 15, 0, 0, 0, time.UTC)
	original := cli.Plan{
		Command: "tool create thing",
		Args: map[string]any{
			"name":  "alpha",
			"count": float64(3), // float64 because JSON numbers decode as float64
		},
		Effects: []cli.Effect{
			{Kind: "create", Target: "thing:alpha", Reversible: true, Detail: "new resource"},
			{Kind: "update", Target: "index", Reversible: false},
		},
		PrerequisitesChecked: []string{"auth", "config"},
		Warnings:             []string{"existing thing will be replaced"},
		GeneratedAt:          now,
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var got cli.Plan
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, original.Command, got.Command)
	assert.Equal(t, original.Args, got.Args)
	assert.Equal(t, original.Effects, got.Effects)
	assert.Equal(t, original.PrerequisitesChecked, got.PrerequisitesChecked)
	assert.Equal(t, original.Warnings, got.Warnings)
	assert.True(t, got.GeneratedAt.Equal(now), "GeneratedAt must round-trip")
}

// TestRoot_DryRun_PolicyAllow_OnDestructive confirms ADR-0020's
// convention: a destructive leaf accepts --dry-run by default
// (resolved via the kit/side-effect tier; no per-command opt-in
// required). The kit-global --dry-run on the root persistent flag
// is the single source of truth; leaves no longer carry a local
// --dry-run flag that would shadow it.
func TestRoot_DryRun_PolicyAllow_OnDestructive(t *testing.T) {
	r := validateRoot()
	nuke := leaf("nuke", cli.SideEffectDestructive)
	r.Cmd.AddCommand(nuke)

	r.AutoRegisterFlags()

	require.NotNil(t, r.Cmd.PersistentFlags().Lookup("dry-run"),
		"kit-global --dry-run must live on the root persistent flag set")
	assert.Nil(t, nuke.Flags().Lookup("dry-run"),
		"per-leaf --dry-run must not shadow the root persistent flag")
	assert.True(t, cli.IsDryRunSupported(nuke),
		"destructive leaves are dry-run-supported by default under ADR-0020")
}

// TestRoot_DryRun_PolicyAllow_OnWrite confirms write leaves are
// dry-run-supported by default under the tier-driven policy.
func TestRoot_DryRun_PolicyAllow_OnWrite(t *testing.T) {
	r := validateRoot()
	create := leaf("create", cli.SideEffectWrite)
	r.Cmd.AddCommand(create)

	r.AutoRegisterFlags()

	assert.True(t, cli.IsDryRunSupported(create),
		"write leaves are dry-run-supported by default under ADR-0020")
}

// TestRoot_DryRun_NotSupported_OnRead confirms read leaves resolve
// to "no-op" under ADR-0020: the flag is accepted silently when
// passed to a read command but the leaf is not "supported" in the
// IsDryRunSupported sense (no help addendum, no ctx tag).
func TestRoot_DryRun_NotSupported_OnRead(t *testing.T) {
	r := validateRoot()
	list := leaf("list", cli.SideEffectRead)
	r.Cmd.AddCommand(list)

	r.AutoRegisterFlags()

	assert.False(t, cli.IsDryRunSupported(list),
		"read-tagged leaves are not dry-run-supported (silent no-op only)")
}

// TestRoot_DryRun_NotSupported_OnInteractive confirms interactive
// leaves reject --dry-run (no batch boundary to scope the preview).
func TestRoot_DryRun_NotSupported_OnInteractive(t *testing.T) {
	r := validateRoot()
	shell := leaf("shell", cli.SideEffectInteractive)
	r.Cmd.AddCommand(shell)

	r.AutoRegisterFlags()

	assert.False(t, cli.IsDryRunSupported(shell),
		"interactive leaves are not dry-run-supported (rejected by hook)")
}

// TestRoot_AutoRegister_Idempotent confirms re-running AutoRegisterFlags
// is safe. The walker is now a no-op for --dry-run (the kit-global
// flag covers every leaf via persistent flags), but it remains the
// registration hook for future kit-managed per-leaf flags.
func TestRoot_AutoRegister_Idempotent(t *testing.T) {
	r := validateRoot()
	create := leaf("create", cli.SideEffectWrite)
	r.Cmd.AddCommand(create)

	r.AutoRegisterFlags()
	r.AutoRegisterFlags() // must not panic

	assert.True(t, cli.IsDryRunSupported(create))
}

// TestRoot_DryRun_AdopterLocalFlag_NotShadowed confirms an adopter
// who pre-declared a local --dry-run on a leaf keeps their flag —
// the kit walker no longer registers a competing flag, so the
// adopter's declaration is the only one cobra sees on that leaf.
func TestRoot_DryRun_AdopterLocalFlag_NotShadowed(t *testing.T) {
	r := validateRoot()
	c := leaf("create", cli.SideEffectWrite)
	c.Flags().Bool("dry-run", true, "adopter-declared dry-run")
	r.Cmd.AddCommand(c)

	r.AutoRegisterFlags()

	flag := c.Flags().Lookup("dry-run")
	require.NotNil(t, flag)
	assert.Equal(t, "true", flag.DefValue,
		"adopter-declared --dry-run must remain intact after AutoRegisterFlags")
}

// TestIsDryRun_True confirms the accessor returns true after the flag
// is set.
func TestIsDryRun_True(t *testing.T) {
	c := leaf("create", cli.SideEffectWrite)
	c.Flags().Bool("dry-run", false, "")
	require.NoError(t, c.Flags().Set("dry-run", "true"))

	assert.True(t, cli.IsDryRun(c))
}

// TestIsDryRun_False confirms the accessor returns false when the flag
// is absent or unset.
func TestIsDryRun_False(t *testing.T) {
	// Flag absent.
	bare := &cobra.Command{Use: "x"}
	assert.False(t, cli.IsDryRun(bare))

	// Flag declared but unset (default).
	c := leaf("create", cli.SideEffectWrite)
	c.Flags().Bool("dry-run", false, "")
	assert.False(t, cli.IsDryRun(c))

	// Nil command must not panic.
	assert.False(t, cli.IsDryRun(nil))
}
