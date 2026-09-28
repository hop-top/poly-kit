package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/cmdreflect"
)

// TestValidateEmptySideEffectIsMissing: an empty kit/side-effect value
// declares nothing, so Validate reports it as missing, the way
// discovery (side_effect_source "unannotated"), spec coverage and
// --dry-run already read it. Reporting it as invalid would print
// `path=""` and send the adopter looking for a typo that is not there.
func TestValidateEmptySideEffectIsMissing(t *testing.T) {
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true})
	leaf := &cobra.Command{
		Use: "blank", Short: "blank", Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "", "kit/idempotent": "yes"},
	}
	r.Cmd.AddCommand(leaf)

	ve := validationErrorOf(t, r)
	assert.Equal(t, []string{"fix blank"}, ve.Missing)
	assert.Empty(t, ve.Invalid, "an empty value names no tier to call malformed")

	d := cmdreflect.Describe(r.Cmd, leaf)
	require.NotNil(t, d)
	assert.Equal(t, cmdreflect.TierUnannotated, d.Safety.Tier,
		"the reflector reads the same value as absent")
}
