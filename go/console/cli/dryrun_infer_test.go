package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/runtime/sideeffect"
)

// runNamedDryRun runs an unannotated leaf called name under --dry-run
// and reports whether RunE saw the dry-run context.
func runNamedDryRun(t *testing.T, name string, decorate func(*cobra.Command)) (bool, bool, error) {
	reached, dry, _, err := runNamedDryRunOut(t, name, nil, decorate)
	return reached, dry, err
}

// runNamedDryRunOut is runNamedDryRun with extra args and the captured
// output.
func runNamedDryRunOut(t *testing.T, name string, extra []string, decorate func(*cobra.Command)) (bool, bool, string, error) {
	t.Helper()
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var reached, dry bool
	leaf := &cobra.Command{
		Use:  name,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reached = true
			dry = sideeffect.IsDryRun(cmd.Context())
			return nil
		},
	}
	if decorate != nil {
		decorate(leaf)
	}
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs(append([]string{name, "--dry-run"}, extra...))
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	err := r.Execute(context.Background())
	return reached, dry, buf.String(), err
}

// An unannotated destructive-named leaf previews under --dry-run: kit
// renders the Plan and never calls the handler, so a handler that
// ignores dry-run cannot mutate, with no --confirm given.
func TestDryRun_InferredDestructiveNamePreviews(t *testing.T) {
	for _, name := range cmdmeta.DestructiveNames() {
		t.Run(name, func(t *testing.T) {
			reached, _, out, err := runNamedDryRunOut(t, name, []string{"--format", "json"}, nil)
			require.NoError(t, err)
			assert.False(t, reached, "handler must not run: it made no dry-run promise")
			assert.Contains(t, out, `"command": "t `+name+`"`)
			assert.Contains(t, out, "inferred destructive-shared")
		})
	}
}

// The legacy opt-in is a promise: the handler runs, as for declared tiers.
func TestDryRun_InferredWithLegacySupportRunsHandler(t *testing.T) {
	reached, dry, err := runNamedDryRun(t, "delete", func(c *cobra.Command) { cli.SupportsDryRun(c) })
	require.NoError(t, err)
	assert.True(t, reached)
	assert.True(t, dry)
}

// Unannotated non-destructive names keep the untagged refusal.
func TestDryRun_UnannotatedOtherNameStillRefused(t *testing.T) {
	reached, _, err := runNamedDryRun(t, "frobnicate", nil)
	require.Error(t, err)
	assert.False(t, reached)
	assert.Contains(t, err.Error(), "kit/side-effect")
}

// An explicit annotation beats the name: a malformed value is still
// refused, an opt-out still wins.
func TestDryRun_InferenceDoesNotOverrideDeclarations(t *testing.T) {
	reached, _, err := runNamedDryRun(t, "delete", func(c *cobra.Command) {
		c.Annotations = map[string]string{"kit/side-effect": "bogus"}
	})
	require.Error(t, err)
	assert.False(t, reached)

	reached, _, err = runNamedDryRun(t, "delete", func(c *cobra.Command) { cli.OptOutDryRun(c) })
	require.Error(t, err)
	assert.False(t, reached)
}

func TestDryRun_InferredDestructiveIsSupported(t *testing.T) {
	c := &cobra.Command{Use: "purge"}
	assert.True(t, cli.IsDryRunSupported(c))
	assert.False(t, cli.IsDryRunSupported(&cobra.Command{Use: "frobnicate"}))
}

// Nothing user-defined that can mutate runs for an inferred leaf under
// --dry-run, whichever way the leaf is wired: Run (not RunE), PreRun,
// PostRun and the leaf's persistent hooks all stay unreached, with no
// --confirm given.
func TestDryRun_InferredLeafRunsNoUserCode(t *testing.T) {
	cases := map[string]func(c *cobra.Command, hit func(string)){
		"Run": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
		},
		"RunE": func(c *cobra.Command, hit func(string)) {
			c.RunE = func(*cobra.Command, []string) error { hit("RunE"); return nil }
		},
		"PreRun": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
			c.PreRun = func(*cobra.Command, []string) { hit("PreRun") }
		},
		"PreRunE": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
			c.PreRunE = func(*cobra.Command, []string) error { hit("PreRunE"); return nil }
		},
		"PostRun": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
			c.PostRun = func(*cobra.Command, []string) { hit("PostRun") }
		},
		"PersistentPreRun": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
			c.PersistentPreRun = func(*cobra.Command, []string) { hit("PersistentPreRun") }
		},
		"PersistentPostRunE": func(c *cobra.Command, hit func(string)) {
			c.Run = func(*cobra.Command, []string) { hit("Run") }
			c.PersistentPostRunE = func(*cobra.Command, []string) error { hit("PersistentPostRunE"); return nil }
		},
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			var hits []string
			r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
			leaf := &cobra.Command{Use: "delete", Args: cobra.NoArgs}
			wire(leaf, func(s string) { hits = append(hits, s) })
			r.Cmd.AddCommand(leaf)
			r.Cmd.SetArgs([]string{"delete", "--dry-run", "--format", "json"})
			var buf bytes.Buffer
			r.Cmd.SetOut(&buf)
			r.Cmd.SetErr(&buf)
			require.NoError(t, r.Execute(context.Background()))
			assert.Empty(t, hits, "user code ran under an inferred --dry-run")
			assert.Contains(t, buf.String(), "inferred destructive-shared")
		})
	}
}

// Without --dry-run a Run-only destructive-named leaf is gated like any
// other: no --confirm, no run.
func TestInferredRunLeafIsConfirmGated(t *testing.T) {
	var ran bool
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{Use: "delete", Run: func(*cobra.Command, []string) { ran = true }})
	r.Cmd.SetArgs([]string{"delete"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	require.Error(t, r.Execute(context.Background()))
	assert.False(t, ran)
}
