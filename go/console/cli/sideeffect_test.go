package cli_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// validateRoot returns a kit Root with no subcommands; tests add their
// own. EnforceValidate is left at its default (false) so Execute won't
// preempt anything; Root.Validate() is invoked directly by each test.
func validateRoot() *cli.Root {
	return cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.1.0",
		Short:           "validation test tool",
		DisableValidate: true,
	})
}

// leaf builds a runnable cobra subcommand with the given side-effect tag
// (or no tag when s == ""). The kit/idempotent tag is auto-set to "yes"
// so the side-effect-focused tests don't trip the idempotency arm of
// Root.Validate; tests that target the idempotency validator
// (TestRoot_Validate_MissingIdempotent_Fails) build leaves directly.
func leaf(name string, s cli.SideEffect) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: name + " command",
		Run:   func(*cobra.Command, []string) {},
	}
	if s != "" {
		cli.SetSideEffect(c, s)
	}
	cli.SetIdempotency(c, cli.IdempotencyYes)
	return c
}

func TestSideEffect_GetSet(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}

	// Missing returns false.
	if _, ok := cli.GetSideEffect(cmd); ok {
		t.Fatalf("expected GetSideEffect to return false on a fresh command")
	}

	// Round-trip via SetSideEffect.
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	got, ok := cli.GetSideEffect(cmd)
	require.True(t, ok, "GetSideEffect must report present after SetSideEffect")
	assert.Equal(t, cli.SideEffectWrite, got)

	// Verify the underlying annotation key is what the spec locks.
	assert.Equal(t, "write", cmd.Annotations["kit/side-effect"])
}

func TestRoot_Validate_AllTagged_OK(t *testing.T) {
	r := validateRoot()
	r.Cmd.AddCommand(leaf("list", cli.SideEffectRead))
	r.Cmd.AddCommand(leaf("create", cli.SideEffectWrite))
	r.Cmd.AddCommand(leaf("delete", cli.SideEffectDestructive))

	require.NoError(t, r.Validate(), "all leaves are tagged; Validate must pass")
}

func TestRoot_Validate_MissingTag_Fails(t *testing.T) {
	r := validateRoot()
	r.Cmd.AddCommand(leaf("list", cli.SideEffectRead))
	r.Cmd.AddCommand(leaf("oops", "")) // untagged
	r.Cmd.AddCommand(leaf("create", cli.SideEffectWrite))

	err := r.Validate()
	require.Error(t, err, "untagged leaf must trigger ValidationError")

	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve), "must be a *ValidationError")
	require.Len(t, ve.Missing, 1)
	assert.Contains(t, ve.Missing[0], "oops",
		"command path of the untagged leaf must surface in Missing")
	assert.Empty(t, ve.Invalid)

	// Error message names the missing path.
	assert.Contains(t, err.Error(), "oops")
}

func TestRoot_Validate_InvalidTag_Fails(t *testing.T) {
	r := validateRoot()
	bad := &cobra.Command{
		Use:   "wipe",
		Short: "wipe everything",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			"kit/side-effect": "rm-rf",
		},
	}
	r.Cmd.AddCommand(bad)

	err := r.Validate()
	require.Error(t, err)

	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve))
	require.Empty(t, ve.Missing)
	require.Len(t, ve.Invalid, 1)
	assert.Contains(t, ve.Invalid[0], "wipe")
	assert.Contains(t, ve.Invalid[0], "rm-rf",
		"the rejected tag value must be quoted in the Invalid entry")
}

func TestRoot_Validate_BuiltinExempt(t *testing.T) {
	r := validateRoot()

	// Manually inject built-in look-alikes lacking the annotation.
	r.Cmd.AddCommand(&cobra.Command{
		Use: "completion", Short: "stand-in completion",
		Run: func(*cobra.Command, []string) {},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "help", Short: "stand-in help",
		Run: func(*cobra.Command, []string) {},
	})
	// One real, tagged leaf so the tree has something to walk past.
	r.Cmd.AddCommand(leaf("list", cli.SideEffectRead))

	require.NoError(t, r.Validate(),
		"completion and help must be exempt from kit/side-effect enforcement")
}

func TestRoot_Validate_NestedLeafs(t *testing.T) {
	r := validateRoot()

	// Parent group: not a leaf, must NOT be checked.
	group := &cobra.Command{
		Use:   "config",
		Short: "config commands",
	}
	group.AddCommand(leaf("show", cli.SideEffectRead))
	group.AddCommand(leaf("set", cli.SideEffectWrite))
	r.Cmd.AddCommand(group)

	// Sibling top-level leaf, also tagged.
	r.Cmd.AddCommand(leaf("ping", cli.SideEffectRead))

	require.NoError(t, r.Validate(),
		"only leaf commands are validated; parent groups are exempt")

	// Now make one nested leaf invalid; Validate must catch it and
	// not flag the parent group.
	bad := &cobra.Command{
		Use:   "rotate",
		Short: "rotate keys",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			"kit/side-effect": "nuke",
		},
	}
	group.AddCommand(bad)

	err := r.Validate()
	require.Error(t, err)
	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve))
	require.Len(t, ve.Invalid, 1)
	assert.True(t,
		strings.Contains(ve.Invalid[0], "config rotate"),
		"nested leaf path must include parent: %q", ve.Invalid[0])
}

func TestRoot_Validate_NonRunnableLeafExempt(t *testing.T) {
	// A "leaf" that has no Run/RunE prints help via cobra; it makes
	// no real-world side-effect and must not be required to declare
	// one. This guards the bare-root case (Root with no children).
	r := validateRoot()
	require.NoError(t, r.Validate(),
		"non-runnable bare root must not trigger missing-tag")
}
