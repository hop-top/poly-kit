package cli_test

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// fixtureRoot returns a kit Root with Config.EnforceValidate=true so
// every Layer-A check fires. The status subcommand is registered by
// default so the reserved-name pass passes; individual tests remove
// it to trigger H6.
func fixtureRoot(t *testing.T, modify ...func(*cli.Config)) *cli.Root {
	t.Helper()
	cfg := cli.Config{
		Name:            "vtool",
		Version:         "0.1.0",
		Short:           "validation fixture tool",
		EnforceValidate: true,
	}
	for _, m := range modify {
		m(&cfg)
	}
	r := cli.New(cfg)
	// Default reserved-status subcommand: a tiny well-formed leaf so
	// individual tests don't have to repeat the boilerplate. The
	// leaf is a depth-1 verb (`<tool> status`) so it needs the
	// kit/top-level-verb annotation under the shape pass.
	statusLeaf := wellFormedLeaf("status", cli.SideEffectRead)
	cli.SetTopLevelVerb(statusLeaf)
	r.Cmd.AddCommand(statusLeaf)
	return r
}

// wellFormedLeaf is a runnable leaf that satisfies every Layer-A
// hard check. Use it as the always-OK baseline and tweak individual
// fields per test.
func wellFormedLeaf(name string, se cli.SideEffect) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: name + " summary",
		Long:  name + " long description for the validator",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(c, se)
	cli.SetIdempotency(c, cli.IdempotencyYes)
	return c
}

// matchValidationError unwraps err to *ValidationError or fails.
func matchValidationError(t *testing.T, err error) *cli.ValidationError {
	t.Helper()
	require.Error(t, err)
	var ve *cli.ValidationError
	require.True(t, errors.As(err, &ve), "expected *cli.ValidationError, got %T: %v", err, err)
	return ve
}

func TestValidate_Matrix_RejectsMissingShort(t *testing.T) {
	r := fixtureRoot(t)
	bad := wellFormedLeaf("ping", cli.SideEffectRead)
	bad.Short = ""
	r.Cmd.AddCommand(bad)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingShort, "missing-Short bucket must populate")
}

func TestValidate_Matrix_RejectsMissingLong(t *testing.T) {
	r := fixtureRoot(t)
	bad := wellFormedLeaf("ping", cli.SideEffectRead)
	bad.Long = ""
	r.Cmd.AddCommand(bad)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingLong)
}

func TestValidate_Matrix_RejectsMissingStatusSubcommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "vtool",
		Version:         "0.1.0",
		Short:           "vt",
		EnforceValidate: true,
		DisableValidate: true,
	})
	// Add a single well-formed leaf BUT no status subcommand.
	r.Cmd.AddCommand(wellFormedLeaf("ping", cli.SideEffectRead))

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingStatusSubcommand,
		"H6: reserved status subcommand must be required")
}

func TestValidate_Matrix_RejectsInvalidOutputSchema(t *testing.T) {
	r := fixtureRoot(t)
	bad := wellFormedLeaf("ping", cli.SideEffectRead)
	if bad.Annotations == nil {
		bad.Annotations = map[string]string{}
	}
	bad.Annotations["kit/output-schema"] = "{not-valid-json"
	bad.Annotations["kit/output-schema-version"] = "1.0"
	r.Cmd.AddCommand(bad)

	ve := matchValidationError(t, r.Validate())
	require.Len(t, ve.InvalidOutputSchema, 1)
	assert.Contains(t, ve.InvalidOutputSchema[0], "ping")
}

func TestValidate_Matrix_RejectsTopLevelLeafWithoutAnnotation(t *testing.T) {
	// kit/top-level-verb required on depth-1 runnable leaves. The
	// default fixture status subcommand DOES require the annotation
	// too — give it the annotation to focus this test on the
	// non-status leaf.
	r := fixtureRoot(t)
	r.Cmd.RemoveCommand(r.Cmd.Commands()...) // clean tree
	statusLeaf := wellFormedLeaf("status", cli.SideEffectRead)
	cli.SetTopLevelVerb(statusLeaf)
	r.Cmd.AddCommand(statusLeaf)

	// Mock an adopter "init" depth-1 leaf without the annotation.
	r.Cmd.AddCommand(wellFormedLeaf("init", cli.SideEffectWrite))

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.UnannotatedTopLevelLeaf,
		"depth-1 leaf without kit/top-level-verb must be flagged")
}

func TestValidate_Matrix_TopLevelLeafAccepted_WhenAnnotated(t *testing.T) {
	r := fixtureRoot(t)
	r.Cmd.RemoveCommand(r.Cmd.Commands()...)
	for _, c := range []*cobra.Command{
		wellFormedLeaf("status", cli.SideEffectRead),
		wellFormedLeaf("init", cli.SideEffectWrite),
	} {
		cli.SetTopLevelVerb(c)
		r.Cmd.AddCommand(c)
	}
	require.NoError(t, r.Validate())
}

func TestValidate_Matrix_RejectsTooManyTopLevelVerbs(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.MaxTopLevelVerbs = 2
	})
	r.Cmd.RemoveCommand(r.Cmd.Commands()...)
	for _, name := range []string{"status", "alpha", "beta", "gamma"} {
		c := wellFormedLeaf(name, cli.SideEffectRead)
		cli.SetTopLevelVerb(c)
		r.Cmd.AddCommand(c)
	}
	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.TooManyTopLevelVerbs)
}

func TestValidate_Matrix_AcceptsCanonicalNounVerb(t *testing.T) {
	r := fixtureRoot(t)
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	group.AddCommand(wellFormedLeaf("create", cli.SideEffectWrite))
	group.AddCommand(wellFormedLeaf("list", cli.SideEffectRead))
	r.Cmd.AddCommand(group)

	require.NoError(t, r.Validate(),
		"depth-2 noun-verb shape is canonical; no shape annotation required")
}

func TestValidate_Matrix_RejectsDepthThreeWithoutHierarchical(t *testing.T) {
	r := fixtureRoot(t)
	outer := &cobra.Command{Use: "foo", Short: "foo group"}
	inner := &cobra.Command{Use: "bar", Short: "bar group"}
	inner.AddCommand(wellFormedLeaf("show", cli.SideEffectRead))
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.UnannotatedDepthExceedance,
		"depth-3 chain without kit/hierarchical must fail")
}

func TestValidate_Matrix_AcceptsDepthThreeWithHierarchical(t *testing.T) {
	r := fixtureRoot(t)
	outer := &cobra.Command{Use: "foo", Short: "foo group"}
	cli.SetHierarchical(outer)
	inner := &cobra.Command{Use: "bar", Short: "bar group"}
	cli.SetHierarchical(inner)
	inner.AddCommand(wellFormedLeaf("show", cli.SideEffectRead))
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	require.NoError(t, r.Validate())
}

func TestValidate_Matrix_RejectsHierarchyDepthExceeded(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.MaxHierarchyDepth = 2
	})
	outer := &cobra.Command{Use: "foo", Short: "foo group"}
	cli.SetHierarchical(outer)
	inner := &cobra.Command{Use: "bar", Short: "bar group"}
	cli.SetHierarchical(inner)
	inner.AddCommand(wellFormedLeaf("show", cli.SideEffectRead))
	outer.AddCommand(inner)
	r.Cmd.AddCommand(outer)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.HierarchyDepthExceeded)
}

func TestValidate_Matrix_EnforceGuidance_RejectsMissingExamples(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceGuidance = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	group.AddCommand(wellFormedLeaf("list", cli.SideEffectRead))
	r.Cmd.AddCommand(group)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingExamples)
}

func TestValidate_Matrix_EnforceGuidance_RejectsMissingNextSteps(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceGuidance = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := wellFormedLeaf("create", cli.SideEffectWrite)
	require.NoError(t, cli.SetExamples(leaf, []cli.Example{
		{Title: "Create one", Command: "vtool foo create --name=x"},
	}))
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingNextSteps,
		"non-read leaf without kit/next-steps must trip EnforceGuidance")
}

func TestValidate_Matrix_EnforceDryRunRationale_RejectsMissingReason(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceDryRunRationale = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := wellFormedLeaf("destroy", cli.SideEffectDestructive)
	cli.OptOutDryRun(leaf) // adopter opts out
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingDryRunRationale)
}

func TestValidate_Matrix_EnforceDryRunRationale_PassesWithReason(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceDryRunRationale = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := wellFormedLeaf("destroy", cli.SideEffectDestructive)
	cli.OptOutDryRun(leaf)
	require.NoError(t, cli.SetDryRunRationale(leaf,
		"destructive ops cannot honor dry-run; partial state would leak"))
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	require.NoError(t, r.Validate())
}

func TestValidate_Matrix_EnforceDestructiveToken_RejectsUnopted(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceDestructiveToken = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	group.AddCommand(wellFormedLeaf("destroy", cli.SideEffectDestructive))
	r.Cmd.AddCommand(group)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.MissingDestructiveToken)
}

func TestValidate_Matrix_EnforceDestructiveToken_PassesWithToken(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.EnforceDestructiveToken = true
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := wellFormedLeaf("destroy", cli.SideEffectDestructive)
	cli.SetDestructiveToken(leaf)
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	require.NoError(t, r.Validate())
}

func TestValidate_Matrix_PassthroughReject_FailsOnAnnotation(t *testing.T) {
	r := fixtureRoot(t, func(c *cli.Config) {
		c.PassthroughStrictness = cli.PassthroughReject
	})
	group := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := wellFormedLeaf("run", cli.SideEffectWrite)
	cli.SetPassthrough(leaf)
	group.AddCommand(leaf)
	r.Cmd.AddCommand(group)

	ve := matchValidationError(t, r.Validate())
	assert.NotEmpty(t, ve.PassthroughRejected)
}

func TestValidate_Matrix_NoEnforce_ShippedChecksStillFire(t *testing.T) {
	// EnforceValidate=false: Layer-A checks silent, but the shipped
	// side-effect arm still rejects missing tags.
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "vt",
		DisableValidate: true,
	})
	bad := &cobra.Command{
		Use: "rotate", Short: "rotate", Run: func(*cobra.Command, []string) {},
	}
	r.Cmd.AddCommand(bad)

	ve := matchValidationError(t, r.Validate())
	require.NotEmpty(t, ve.Missing,
		"shipped kit/side-effect check runs regardless of EnforceValidate")
	require.Empty(t, ve.MissingStatusSubcommand,
		"H6 must be silent when EnforceValidate=false")
}

func TestValidate_Matrix_FullyAnnotatedTreePasses(t *testing.T) {
	r := fixtureRoot(t)
	// Top-level verb leaf.
	tl := wellFormedLeaf("init", cli.SideEffectWrite)
	cli.SetTopLevelVerb(tl)
	r.Cmd.AddCommand(tl)
	// Canonical noun-verb tree.
	foo := &cobra.Command{Use: "foo", Short: "foo group"}
	foo.AddCommand(wellFormedLeaf("create", cli.SideEffectWrite))
	foo.AddCommand(wellFormedLeaf("list", cli.SideEffectRead))
	r.Cmd.AddCommand(foo)

	require.NoError(t, r.Validate(),
		"a fully-annotated tree must validate clean")
}

func TestValidate_Matrix_AsCLIError_ReturnsUsageEnvelope(t *testing.T) {
	ve := &cli.ValidationError{
		MissingShort: []string{"vtool foo"},
	}
	out := ve.AsCLIError()
	require.NotNil(t, out)
	assert.Equal(t, 2, out.ExitCode, "ValidationError must surface USAGE exit code")
}
