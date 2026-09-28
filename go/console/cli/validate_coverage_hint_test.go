package cli

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unannotatedRoot returns a root whose one leaf declares no
// kit/side-effect, so Validate fills the Missing bucket.
func unannotatedRoot(t *testing.T) *Root {
	t.Helper()
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "silent", Short: "silent", Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/idempotent": "yes"},
	})
	return r
}

// mountFakeSpecCoverage stands in for toolspec/cli.RegisterSpecCommand,
// which this package cannot import: what Validate looks for is the
// `spec coverage` command in the tree, not who mounted it.
func mountFakeSpecCoverage(r *Root) {
	ann := map[string]string{"kit/side-effect": "read", "kit/idempotent": "yes"}
	spec := &cobra.Command{Use: "spec", Short: "spec", Annotations: ann,
		Run: func(*cobra.Command, []string) {}}
	spec.AddCommand(&cobra.Command{Use: "coverage", Short: "coverage", Annotations: ann,
		Run: func(*cobra.Command, []string) {}})
	r.Cmd.AddCommand(spec)
}

func validationErrorOf(t *testing.T, r *Root) *ValidationError {
	t.Helper()
	err := r.Validate()
	require.Error(t, err)
	var ve *ValidationError
	require.True(t, errors.As(err, &ve), "want *ValidationError, got %T", err)
	return ve
}

// TestValidatePointsAtSpecCoverage: a missing side-effect refusal
// names the command that tracks the gap, so an adopter who cannot
// annotate everything today learns how to measure and gate progress.
func TestValidatePointsAtSpecCoverage(t *testing.T) {
	r := unannotatedRoot(t)
	mountFakeSpecCoverage(r)
	ve := validationErrorOf(t, r)

	require.Equal(t, []string{"fix silent"}, ve.Missing)
	assert.Contains(t, ve.Error(), `run "fix spec coverage"`)
	assert.NotContains(t, ve.Error(), "RegisterSpecCommand",
		"a mounted command needs no mounting instructions")
	assert.Contains(t, ve.AsCLIError().Message, `run "fix spec coverage"`,
		"the structured envelope carries the same pointer")
}

// TestValidateSaysHowToMountSpecCoverage: without the spec command
// the pointer must not name a command the binary does not have.
func TestValidateSaysHowToMountSpecCoverage(t *testing.T) {
	ve := validationErrorOf(t, unannotatedRoot(t))

	assert.Contains(t, ve.Error(), "RegisterSpecCommand",
		"an unmounted spec coverage must be named with how to mount it")
	assert.NotContains(t, ve.Error(), `run "fix spec coverage"`)
}

// TestValidateCoverageHintOnlyForSideEffect: the pointer belongs to
// the side-effect buckets; other refusals keep their message as is.
func TestValidateCoverageHintOnlyForSideEffect(t *testing.T) {
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "odd", Short: "odd", Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "maybe"},
	})
	mountFakeSpecCoverage(r)
	ve := validationErrorOf(t, r)

	require.Empty(t, ve.Missing)
	require.NotEmpty(t, ve.InvalidIdempotency)
	assert.NotContains(t, ve.Error(), "spec coverage")
}

// TestValidateCoverageHintForMalformedSideEffect: a typo is a coverage
// miss too, so it gets the same pointer.
func TestValidateCoverageHintForMalformedSideEffect(t *testing.T) {
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "typo", Short: "typo", Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "destrutive", "kit/idempotent": "no"},
	})
	mountFakeSpecCoverage(r)
	ve := validationErrorOf(t, r)

	require.NotEmpty(t, ve.Invalid)
	assert.Contains(t, ve.Error(), `"fix spec coverage"`)
}
