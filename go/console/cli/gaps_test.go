package cli_test

// Tests in this file pin behavior for API gaps that the c12n review
// surfaced in `hop.top/kit/go/console/cli`. The skip-stubs that lived
// here originally have been replaced with real assertions as the gaps
// were closed; see docs/audits/known-parity-gaps.md for the gap log.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// Gap 1 (closed): cli.Config exposes Hooks.PrePersistentRunE as an
// additive slot. Adopters that need to run code after kit's built-in
// chain (chdir → identity → peer init) but before subcommand RunE wire
// it through this slot instead of overwriting r.Cmd.PersistentPreRunE
// — which silently clobbered kit's hooks. The chain composer in New()
// runs every registered hook in order and short-circuits on first
// error.
func TestGap_HooksPrePersistentRunESlot_Missing(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	hookRan := false
	r := cli.New(cli.Config{
		Name: "gap1", Version: "0.0.0", Short: "gap",
		Hooks: cli.Hooks{
			PrePersistentRunE: func(_ *cobra.Command, _ []string) error {
				hookRan = true
				return nil
			},
		},
		DisableValidate: true,
	})
	require.NotNil(t, r.Cmd.PersistentPreRunE, "kit installs a chain composer")

	// Drive the composed chain: --chdir <dir> proves kit's chdir hook
	// still ran, and hookRan proves the adopter hook also ran.
	require.NoError(t, r.Cmd.ParseFlags([]string{"--chdir", dir}))
	require.NoError(t, r.Cmd.PersistentPreRunE(r.Cmd, nil))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal, "kit's chdir hook must still run before the adopter hook")
	assert.True(t, hookRan, "adopter hook must also run as part of the composed chain")
}

// Gap 1b (closed): peer-init failure preserves the rest of the chain.
//
// Previously, when initPeers() returned an error, New() overwrote
// PersistentPreRunE with a single closure that returned that error,
// dropping the chdir hook. The new chain composer collects all hooks
// into a slice and assigns PersistentPreRunE exactly once; chdir runs
// before the peer-error short-circuit, exposing the failure to the
// caller without losing the prior chain.
//
// The deterministic seam used here is WithPeers without WithIdentity:
// initPeers() returns "peer: WithPeers requires WithIdentity" without
// touching disk or starting goroutines.
func TestGap_PeerInitFailure_DropsChdirHook(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{Name: "gap1b", Version: "0.0.0", Short: "gap", DisableValidate: true},
		cli.WithPeers(cli.PeerConfig{DataDir: dir}))
	require.NotNil(t, r.Cmd.PersistentPreRunE,
		"chain composer must install PersistentPreRunE even on peer-init failure")

	require.NoError(t, r.Cmd.ParseFlags([]string{"--chdir", dir}))
	err = r.Cmd.PersistentPreRunE(r.Cmd, nil)

	// chdir must have run before the peer-init failure surfaced.
	cwd, cwdErr := os.Getwd()
	require.NoError(t, cwdErr)
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal,
		"chdir hook must run before peer-init error short-circuits the chain")

	require.Error(t, err, "peer-init error must still surface")
	assert.Contains(t, err.Error(), "WithPeers requires WithIdentity")
}

// Gap 2 (closed): cli.Flag accepts value-pointer destinations.
//
// Adopters can now declare a typed pointer (StringVar / BoolVar /
// IntVar) on the Flag literal and read the parsed value directly,
// without a side trip through viper. Viper is still bound for
// adopters that prefer config-file precedence.
func TestGap_Globals_ValuePointerDestination_Missing(t *testing.T) {
	var experiment bool
	r := cli.New(cli.Config{
		Name:    "gap2",
		Version: "0.0.0",
		Short:   "gap",
		Globals: []cli.Flag{
			// "experiment" stands in for any tool-specific persistent
			// flag. The previous fixture used "dry-run", but kit now
			// reserves --dry-run as a built-in global; this test still
			// covers the BoolVar pointer-destination contract under a
			// non-reserved flag name.
			{Name: "experiment", Usage: "experiment", Default: "false", BoolVar: &experiment},
		},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	require.NotNil(t, pf.Lookup("experiment"))

	require.NoError(t, r.Cmd.ParseFlags([]string{"--experiment"}))
	assert.True(t, experiment, "BoolVar pointer must receive parsed flag value")
	assert.True(t, r.Viper.GetBool("experiment"), "viper must remain wired for the same flag")
}

// pin: the legacy cli.Flag literal (no value-pointer fields) must keep
// compiling so existing adopters don't break.
var _ = cli.Flag{Name: "_", Usage: "_", Default: "_"}

// pin: the new pointer-destination fields are part of the Flag literal
// surface and stay zero when unused.
var _ = cli.Flag{StringVar: nil, BoolVar: nil, IntVar: nil}

// pin: the additive Hooks.PrePersistentRunE slot must accept the cobra
// PersistentPreRunE signature.
var _ = cli.Hooks{PrePersistentRunE: func(*cobra.Command, []string) error { return nil }}
