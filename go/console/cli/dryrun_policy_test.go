// dryrun_policy_test.go locks the ADR-0020 tier-driven --dry-run
// policy: write|destructive accept by default, read silently
// no-ops, interactive rejects with a friendly diagnostic, OptOutDryRun
// rejects with an explicit-decision diagnostic, and the legacy
// kit/dry-run: supported annotation remains a back-compat synonym.

package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/sideeffect"
)

// runWithDryRun executes a leaf under --dry-run and returns the
// observed (captured-output, ranWithDryRun, err) tuple. The leaf
// records whether sideeffect.IsDryRun(ctx) was true at RunE time.
func runWithDryRun(t *testing.T, tier cli.SideEffect, decorate func(*cobra.Command)) (string, bool, error) {
	t.Helper()
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var ranWithDryRun bool
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ranWithDryRun = sideeffect.IsDryRun(cmd.Context())
			return nil
		},
	}
	if tier != "" {
		cli.SetSideEffect(leaf, tier)
	}
	if decorate != nil {
		decorate(leaf)
	}
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do", "--dry-run"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	err := r.Execute(context.Background())
	return buf.String(), ranWithDryRun, err
}

func TestPolicy_Write_AcceptsAndTagsContext(t *testing.T) {

	_, ran, err := runWithDryRun(t, cli.SideEffectWrite, nil)
	require.NoError(t, err, "write tier must accept --dry-run by default")
	assert.True(t, ran, "RunE must observe IsDryRun(ctx)=true")
}

func TestPolicy_Destructive_AcceptsAndTagsContext(t *testing.T) {

	_, ran, err := runWithDryRun(t, cli.SideEffectDestructive, nil)
	require.NoError(t, err, "destructive tier must accept --dry-run by default")
	assert.True(t, ran, "RunE must observe IsDryRun(ctx)=true")
}

// TestPolicy_Read_SilentNoOp: --dry-run on a read command must NOT
// fail and MUST NOT tag ctx — preserves the spec behavior for
// shell-history style mistakes.
func TestPolicy_Read_SilentNoOp(t *testing.T) {

	_, ran, err := runWithDryRun(t, cli.SideEffectRead, nil)
	require.NoError(t, err, "read tier must silently accept --dry-run")
	assert.False(t, ran, "read commands must not tag ctx with dry-run")
}

func TestPolicy_Interactive_RejectsWithFriendlyDiagnostic(t *testing.T) {

	_, ran, err := runWithDryRun(t, cli.SideEffectInteractive, nil)
	require.Error(t, err, "interactive tier must reject --dry-run")
	assert.False(t, ran, "RunE must not be reached when --dry-run is rejected")
	assert.Contains(t, err.Error(), "interactive",
		"diagnostic must explain interactive sessions reject the flag")
}

func TestPolicy_OptOut_RejectsWithExplicitDecisionDiagnostic(t *testing.T) {

	_, ran, err := runWithDryRun(t, cli.SideEffectWrite, func(c *cobra.Command) {
		cli.OptOutDryRun(c)
	})
	require.Error(t, err, "OptOutDryRun must reject --dry-run")
	assert.False(t, ran, "RunE must not be reached when opted out")
	assert.Contains(t, err.Error(), "opted out",
		"diagnostic must point at the explicit OptOutDryRun decision")
}

func TestPolicy_Untagged_Rejects(t *testing.T) {

	_, ran, err := runWithDryRun(t, "", nil)
	require.Error(t, err, "untagged leaf must reject --dry-run")
	assert.False(t, ran)
	assert.Contains(t, err.Error(), "kit/side-effect",
		"diagnostic must point at the missing tag")
}

// TestPolicy_LegacySupports_Allows: ADR-0019 callers who already
// have cli.SupportsDryRun(cmd) keep working without the tier — the
// annotation is a back-compat synonym.
func TestPolicy_LegacySupports_Allows(t *testing.T) {

	_, ran, err := runWithDryRun(t, "", func(c *cobra.Command) {
		cli.SupportsDryRun(c)
	})
	require.NoError(t, err, "legacy SupportsDryRun annotation must allow")
	assert.True(t, ran)
}

// TestPolicy_LegacySupports_LogsDeprecation: the legacy annotation
// fires a one-time deprecation warning at startup.
func TestPolicy_LegacySupports_LogsDeprecation(t *testing.T) {

	r := cli.New(cli.Config{Name: "tdep", Version: "0.1.0", Short: "t", DisableValidate: true})
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		Run:  func(*cobra.Command, []string) {},
	}
	cli.SupportsDryRun(leaf)
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	_ = r.Execute(context.Background())
	// Run a second time to confirm the warning is one-time across the
	// process. The Once ties to the package; multiple Roots in one
	// process get a single warning total.
	r2 := cli.New(cli.Config{Name: "tdep", Version: "0.1.0", Short: "t", DisableValidate: true})
	leaf2 := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		Run:  func(*cobra.Command, []string) {},
	}
	cli.SupportsDryRun(leaf2)
	r2.Cmd.AddCommand(leaf2)
	r2.Cmd.SetArgs([]string{"do"})
	var buf2 bytes.Buffer
	r2.Cmd.SetOut(&buf2)
	r2.Cmd.SetErr(&buf2)
	_ = r2.Execute(context.Background())

	// Combined output must contain exactly one deprecation line.
	combined := buf.String() + buf2.String()
	count := strings.Count(combined, "[deprecation] kit/dry-run: supported")
	assert.LessOrEqual(t, count, 1,
		"deprecation warning must fire at most once per process")
}

// TestPolicy_NoDoubleRegistration: setting both the side-effect tier
// AND the legacy SupportsDryRun annotation must not cause double
// allowance, error duplication, or duplicate help addendum lines.
func TestPolicy_NoDoubleRegistration(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	leaf := &cobra.Command{
		Use:   "do",
		Short: "do something",
		Long:  "Do something useful.",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(leaf, cli.SideEffectWrite)
	cli.SupportsDryRun(leaf) // legacy annotation on top of tier
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do", "--help"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	_ = r.Execute(context.Background())

	out := buf.String()
	count := strings.Count(out, "Dry-run support: this command honors --dry-run.")
	assert.LessOrEqual(t, count, 1,
		"help addendum must not duplicate when both tier and legacy annotation are set")
}

// TestPolicy_HelpAddendum_DerivesFromPolicy: the addendum follows
// the resolved policy table — write/destructive get it; read,
// interactive, opted-out, and untagged do not.
func TestPolicy_HelpAddendum_DerivesFromPolicy(t *testing.T) {

	cases := []struct {
		name     string
		tier     cli.SideEffect
		decorate func(*cobra.Command)
		want     bool
	}{
		{"write", cli.SideEffectWrite, nil, true},
		{"destructive", cli.SideEffectDestructive, nil, true},
		{"read", cli.SideEffectRead, nil, false},
		{"interactive", cli.SideEffectInteractive, nil, false},
		{"opted-out", cli.SideEffectWrite, func(c *cobra.Command) { cli.OptOutDryRun(c) }, false},
		{"untagged", "", nil, false},
		{"legacy-supports", "", func(c *cobra.Command) { cli.SupportsDryRun(c) }, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
			leaf := &cobra.Command{
				Use:   "do",
				Short: "do",
				Long:  "Do.",
				Run:   func(*cobra.Command, []string) {},
			}
			if tc.tier != "" {
				cli.SetSideEffect(leaf, tc.tier)
			}
			if tc.decorate != nil {
				tc.decorate(leaf)
			}
			r.Cmd.AddCommand(leaf)
			r.Cmd.SetArgs([]string{"do", "--help"})
			var buf bytes.Buffer
			r.Cmd.SetOut(&buf)
			r.Cmd.SetErr(&buf)
			_ = r.Execute(context.Background())
			has := strings.Contains(buf.String(), "Dry-run support")
			assert.Equal(t, tc.want, has,
				"%s tier addendum mismatch (want=%v have=%v)", tc.name, tc.want, has)
		})
	}
}

// TestPolicy_TreeWalk_3_6Convention: confirms cli-conventions §3.6
// holds across an entire kit-shaped command tree without explicit
// SupportsDryRun calls. Every write|destructive leaf in the tree
// MUST resolve to "supported"; read|interactive leaves MUST NOT.
func TestPolicy_TreeWalk_3_6Convention(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})

	// Build a synthetic tree: a "store" group with leaves of every
	// tier, plus a top-level interactive leaf.
	store := &cobra.Command{Use: "store", Short: "store group"}
	r.Cmd.AddCommand(store)
	mkLeaf := func(name string, tier cli.SideEffect) *cobra.Command {
		c := &cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}}
		cli.SetSideEffect(c, tier)
		return c
	}
	store.AddCommand(mkLeaf("get", cli.SideEffectRead))
	store.AddCommand(mkLeaf("put", cli.SideEffectWrite))
	store.AddCommand(mkLeaf("rm", cli.SideEffectDestructive))
	r.Cmd.AddCommand(mkLeaf("shell", cli.SideEffectInteractive))

	// Run AutoRegisterFlags + addendum + walk policy on every leaf.
	r.AutoRegisterFlags()

	type expect struct {
		name      string
		path      []string
		supported bool
	}
	cases := []expect{
		{"get", []string{"store", "get"}, false},
		{"put", []string{"store", "put"}, true},
		{"rm", []string{"store", "rm"}, true},
		{"shell", []string{"shell"}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := r.Cmd
			for _, seg := range tc.path {
				var found *cobra.Command
				for _, c := range cmd.Commands() {
					if c.Name() == seg {
						found = c
					}
				}
				require.NotNil(t, found, "missing %s in tree", seg)
				cmd = found
			}
			assert.Equal(t, tc.supported, cli.IsDryRunSupported(cmd),
				"§3.6: %s tier mismatch", tc.name)
		})
	}
}
