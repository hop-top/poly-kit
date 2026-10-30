package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWalkCobra_ContractRetryable pins that kit/retryable reaches
// Contract.Retryable, alone or alongside the other contract facts.
func TestWalkCobra_ContractRetryable(t *testing.T) {
	root := rootWith("mytool",
		addChild("retry", func(c *cobra.Command) {
			c.Annotations = map[string]string{"kit/retryable": "true"}
		}),
		addChild("both", func(c *cobra.Command) {
			c.Annotations = map[string]string{
				"kit/side-effect": "read",
				"kit/idempotent":  "yes",
				"kit/retryable":   "true",
			}
		}),
		addChild("neither", func(c *cobra.Command) {
			c.Annotations = map[string]string{"kit/retryable": "false", "kit/side-effect": "read"}
		}),
	)
	spec := WalkCobra(root)

	retry := findCommand(spec.Commands, "retry")
	require.NotNil(t, retry.Contract, "kit/retryable alone declares a contract")
	assert.True(t, retry.Contract.Retryable)
	assert.False(t, retry.Contract.Idempotent)

	both := findCommand(spec.Commands, "both")
	require.NotNil(t, both.Contract)
	assert.True(t, both.Contract.Retryable)
	assert.True(t, both.Contract.Idempotent)
	assert.Equal(t, []string{"read"}, both.Contract.SideEffects)

	neither := findCommand(spec.Commands, "neither")
	require.NotNil(t, neither.Contract)
	assert.False(t, neither.Contract.Retryable)
}
