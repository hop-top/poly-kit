package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

// denyingEngineErr builds a real CEL-backed engine whose only policy
// denies, and returns the error Decide produces. No hand-built
// PolicyDeniedError: the test has to see what the engine actually
// returns at runtime.
func denyingEngineErr(t *testing.T) error {
	t.Helper()
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: admin-only-cancel
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "only admins may cancel"
`))
	require.NoError(t, err)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{}, "resource": map[string]any{},
		"context": map[string]any{}, "payload": map[string]any{},
	})
	require.Error(t, err)
	return err
}

// runDenialThroughCLI wires the denial into a kit CLI leaf command and
// runs it through the real RunE middleware chain (cli.Root.WrapRunE),
// returning the rendered stderr and the error cobra surfaced. This is
// the actual path a consumer's `main` takes — not a direct call to
// AsCLIError, and not a helper standing in for the middleware.
func runDenialThroughCLI(t *testing.T, runErr error) (string, error) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "policytool",
		Version:         "0.0.0",
		Short:           "policy denial envelope test tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use:         "cancel",
		Short:       "cancel a thing",
		Annotations: map[string]string{"kit/side-effect": "false"},
		RunE: func(_ *cobra.Command, _ []string) error {
			return runErr
		},
	}
	r.Cmd.AddCommand(leaf)

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"cancel", "--format", "json"})
	r.WrapRunE()

	err := r.Cmd.Execute()
	return stderr.String(), err
}

// A policy denial returned from a command handler must render as a
// CONFLICT / exit-4 envelope. Before AsCLIError existed the middleware
// found no conversion and fell through to GENERIC / exit 1, silently
// downgrading every denial.
func TestPolicyDenial_RendersConflictEnvelopeThroughRunEMiddleware(t *testing.T) {
	denial := denyingEngineErr(t)

	stderr, err := runDenialThroughCLI(t, denial)
	require.Error(t, err)

	var got output.Error
	require.NoError(t, json.Unmarshal([]byte(stderr), &got),
		"expected JSON envelope on stderr, got %q", stderr)

	assert.Equal(t, output.CodeConflict, got.Code,
		"policy denial must render as CONFLICT, not the generic bucket")
	assert.Equal(t, 4, got.ExitCode,
		"policy denial must carry exit 4; exit 1 means AsCLIError was not found")
	assert.Equal(t, output.TransiencePermanent, got.Transience)
	assert.Contains(t, got.Message, "only admins may cancel")
	assert.Contains(t, got.Cause, "admin-only-cancel",
		"envelope must name the denying policy so an operator can find the rule")
	assert.Contains(t, got.Cause, "kit.runtime.entity.pre_validated")
	assert.Contains(t, got.SuggestedFix, "admin-only-cancel")

	// The exit code the process would use comes off the error the
	// middleware returns, not just the rendered bytes.
	var env *output.Error
	require.True(t, errors.As(err, &env),
		"middleware must return an *output.Error carrying the exit code")
	assert.Equal(t, 4, env.ExitCode)
}

// The conversion must not sever sentinel matching: errors.Is has to keep
// working both on the raw denial and through the envelope the middleware
// hands back.
func TestPolicyDenial_ErrConflictSurvivesEnvelopeConversion(t *testing.T) {
	denial := denyingEngineErr(t)
	require.True(t, errors.Is(denial, domain.ErrConflict),
		"raw denial must still match domain.ErrConflict")

	_, err := runDenialThroughCLI(t, denial)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict),
		"errors.Is(domain.ErrConflict) must survive the envelope conversion")

	var pde *policy.PolicyDeniedError
	assert.True(t, errors.As(err, &pde),
		"errors.As must still reach *PolicyDeniedError through the envelope")
}
