package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/sideeffect"
)

// The confirm gate skips its question under --dry-run only because the
// leaf contracted to preview instead of act. These tests pin that the
// skip is granted on the strength of that contract — the dry-run policy
// resolving "allow" for the leaf — and never on the bare flag.

// sideEffectLeaf is a runnable leaf of tier se whose RunE records that
// it ran. The recorded bool is the side effect the gates must keep from
// happening.
func sideEffectLeaf(name string, se SideEffect, ran *bool) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: name + " command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*ran = true
			_, _ = cmd.OutOrStdout().Write([]byte("ran:" + name))
			return nil
		},
	}
	SetSideEffect(c, se)
	SetIdempotency(c, IdempotencyYes)
	return c
}

// An opted-out destructive leaf reached through a group whose own
// PersistentPreRunE shadows kit's chain: the pre-execution dry-run hook
// never runs, so the refusal has to come from the RunE gate itself.
// Before the fix the bare --dry-run flag skipped the confirm gate and
// the leaf deleted for real.
func TestDryRunConfirm_HookShadowed_OptedOutLeafRefused(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	group := &cobra.Command{
		Use:               "pattern",
		Short:             "pattern group",
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	var ran bool
	leaf := sideEffectLeaf("delete", SideEffectDestructive, &ran)
	OptOutDryRun(leaf)
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	stdout, stderr, err := runWithStdin(t, r,
		[]string{"pattern", "delete", "--dry-run", "--confirm", "yes", "--format", "json"},
		"", false)
	require.Error(t, err, "opted-out leaf must refuse --dry-run")
	assert.False(t, ran, "RunE must not run when --dry-run is refused")
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "opted out")
}

// Same shadowed hook, a leaf that does not honour --dry-run and never
// opted out either: an interactive one. Without the hook the confirm
// skip used to be granted on the flag alone.
func TestDryRunConfirm_HookShadowed_InteractiveLeafRefused(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	group := &cobra.Command{
		Use:               "shell",
		Short:             "shell group",
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	var ran bool
	group.AddCommand(sideEffectLeaf("open", SideEffectInteractive, &ran))
	r.Cmd.AddCommand(group)

	_, _, err := runWithStdin(t, r,
		[]string{"shell", "open", "--dry-run"}, "", false)
	require.Error(t, err)
	assert.False(t, ran)
}

// With the hook shadowed, a leaf that does honour --dry-run still gets
// the preview: the gate tags the context itself, so RunE sees dry-run
// through either accessor, and the confirm question is not asked.
func TestDryRunConfirm_HookShadowed_HonouringLeafPreviews(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	group := &cobra.Command{
		Use:               "pattern",
		Short:             "pattern group",
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	var sawCtx, deleted bool
	leaf := &cobra.Command{
		Use: "delete", Short: "delete", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sawCtx = sideeffect.IsDryRun(cmd.Context())
			if IsDryRun(cmd) {
				return nil
			}
			deleted = true
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectDestructive)
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	_, prompt, err := runWithStdin(t, r,
		[]string{"pattern", "delete", "--dry-run"}, "y\n", true)
	require.NoError(t, err)
	assert.True(t, sawCtx, "the gate must tag the context when the hook did not")
	assert.False(t, deleted)
	assert.NotContains(t, prompt, "Continue?")
}

// A tool that suppressed kit's --dry-run and declares its own: kit
// cannot know whether the leaf honours it, so the destructive leaf
// keeps its confirm gate. Before the fix the adopter's flag alone
// skipped the gate.
func TestDryRunConfirm_KitDryRunDisabled_LocalFlagKeepsGate(t *testing.T) {
	r := New(Config{
		Name: "ptool", Version: "0.0.0", Short: "p",
		Disable: Disable{DryRun: true},
	})
	var ran bool
	leaf := sideEffectLeaf("delete", SideEffectDestructive, &ran)
	leaf.Flags().Bool("dry-run", false, "adopter-owned dry-run")
	r.Cmd.AddCommand(leaf)

	_, stderr, err := runWithStdin(t, r,
		[]string{"delete", "--dry-run", "--confirm", "no", "--format", "json"},
		"", false)
	require.Error(t, err, "confirm gate must still refuse")
	assert.False(t, ran)
	assert.Contains(t, stderr, "refused")
}

// A leaf kit exempts from validation is still a runnable leaf: the
// dry-run policy applies to it like to any other. Before the fix the
// hook skipped exempt leaves as if they were help or completion, so an
// opted-out exempt leaf ran for real under --dry-run.
func TestDryRunConfirm_ExemptValidationLeaf_PolicyApplies(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	var ran bool
	leaf := sideEffectLeaf("mint", SideEffectWriteShared, &ran)
	SetExemptValidation(leaf)
	OptOutDryRun(leaf)
	r.Cmd.AddCommand(leaf)

	_, _, err := runWithStdin(t, r, []string{"mint", "--dry-run"}, "", false)
	require.Error(t, err)
	assert.False(t, ran)
}

// The honouring path end to end: a destructive leaf that previews
// under --dry-run renders a Plan, is not asked to confirm, and does
// not perform its effect. Without --dry-run the same leaf is asked as
// before, and declining keeps the effect from happening.
func TestDryRunConfirm_HonouringLeaf_PlanNoPrompt(t *testing.T) {
	newRoot := func(deleted *bool) *Root {
		r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
		leaf := &cobra.Command{
			Use: "delete <name>", Short: "delete", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if IsDryRun(cmd) {
					return RenderPlan(cmd, Plan{Effects: []Effect{
						{Kind: "delete", Target: "pattern:" + args[0]},
					}})
				}
				*deleted = true
				return nil
			},
		}
		SetSideEffect(leaf, SideEffectDestructive)
		r.Cmd.AddCommand(leaf)
		return r
	}

	t.Run("dry-run previews", func(t *testing.T) {
		var deleted bool
		r := newRoot(&deleted)
		stdout, prompt, err := runWithStdin(t, r,
			[]string{"delete", "x", "--dry-run", "--format", "json"}, "y\n", true)
		require.NoError(t, err)
		assert.False(t, deleted, "dry-run must not delete")
		assert.NotContains(t, prompt, "Continue?", "no confirm question under an honoured dry-run")
		var p Plan
		require.NoError(t, json.Unmarshal([]byte(stdout), &p), "stdout=%q", stdout)
		assert.Equal(t, "ptool delete", p.Command)
		require.Len(t, p.Effects, 1)
		assert.Equal(t, "pattern:x", p.Effects[0].Target)
		assert.False(t, p.GeneratedAt.IsZero())
	})

	t.Run("no dry-run confirms", func(t *testing.T) {
		var deleted bool
		r := newRoot(&deleted)
		_, prompt, err := runWithStdin(t, r, []string{"delete", "x"}, "n\n", true)
		require.Error(t, err)
		assert.Contains(t, prompt, "Continue?")
		assert.False(t, deleted)
	})
}

// --dry-run from config or KIT_DRY_RUN reaches the leaf as a tagged
// context, not as a set flag. IsDryRun must report it, or a leaf that
// asks IsDryRun would act for real while kit skipped its confirm gate.
func TestIsDryRun_ReportsTaggedContext(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	c.SetContext(sideeffect.WithDryRun(context.Background(), true))
	assert.True(t, IsDryRun(c))
}

func TestDryRunConfirm_DryRunFromConfig_LeafSeesIt(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	r.Viper.Set(globalDryRunViperKey, true)
	var deleted, asked bool
	leaf := &cobra.Command{
		Use: "delete", Short: "delete", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asked = true
			if IsDryRun(cmd) {
				return nil
			}
			deleted = true
			return nil
		},
	}
	SetSideEffect(leaf, SideEffectDestructive)
	r.Cmd.AddCommand(leaf)

	_, _, err := runWithStdin(t, r, []string{"delete"}, "", false)
	require.NoError(t, err)
	assert.True(t, asked)
	assert.False(t, deleted, "config-driven dry-run must reach IsDryRun")
}

// fang styles a bare error by title-casing its first word, which turned
// "--dry-run" into "--Dry-Run". The refusal is a kit envelope now, so
// kit renders it and the flag keeps its spelling.
func TestDryRunRefusal_FlagNameKeepsCase(t *testing.T) {
	cases := []struct {
		name     string
		decorate func(*cobra.Command)
		se       SideEffect
	}{
		{"opted out", OptOutDryRun, SideEffectDestructive},
		{"interactive", nil, SideEffectInteractive},
		{"untagged", nil, ""},
		{"malformed", func(c *cobra.Command) { c.Annotations[sideEffectAnnotation] = "bogus" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p", DisableValidate: true})
			leaf := &cobra.Command{
				Use: "do", Short: "do", Args: cobra.NoArgs,
				Annotations: map[string]string{},
				RunE: func(*cobra.Command, []string) error {
					t.Fatal("RunE must not run")
					return nil
				},
			}
			if tc.se != "" {
				SetSideEffect(leaf, tc.se)
			}
			if tc.decorate != nil {
				tc.decorate(leaf)
			}
			r.Cmd.AddCommand(leaf)
			var out, errb bytes.Buffer
			r.Cmd.SetOut(&out)
			r.Cmd.SetErr(&errb)
			r.Cmd.SetArgs([]string{"do", "--dry-run"})
			err := r.Execute(context.Background())
			require.Error(t, err)
			assert.Contains(t, errb.String(), "--dry-run")
			assert.NotContains(t, errb.String(), "--Dry-Run")

			var ce *output.Error
			require.ErrorAs(t, err, &ce, "refusal must be a kit envelope")
			assert.Equal(t, output.CodeUsage, ce.Code)
			assert.False(t, strings.HasPrefix(ce.Message, "--"),
				"message must not lead with the flag: renderers capitalise the first word")
		})
	}
}

// An alias shim for a nested destructive command dispatches the
// target's RunE, so it must carry the target's annotations: the confirm
// gate and the dry-run policy read the shim, not the target.
func TestAliasShim_InheritsGates(t *testing.T) {
	r := New(Config{Name: "ptool", Version: "0.0.0", Short: "p"})
	group := &cobra.Command{Use: "pattern", Short: "pattern group"}
	var ran bool
	leaf := sideEffectLeaf("delete", SideEffectDestructive, &ran)
	OptOutDryRun(leaf)
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)
	require.NoError(t, r.Alias("pd", leaf))

	_, _, err := runWithStdin(t, r, []string{"pd", "--confirm", "no"}, "", false)
	require.Error(t, err, "shim must keep the confirm gate")
	assert.False(t, ran)

	_, _, err = runWithStdin(t, r, []string{"pd", "--dry-run", "--confirm", "yes"}, "", false)
	require.Error(t, err, "shim must keep the dry-run opt-out")
	assert.False(t, ran)
}
