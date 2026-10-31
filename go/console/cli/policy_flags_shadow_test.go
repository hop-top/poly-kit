package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A leaf that defines its own local --policy shadows the root's global
// delegation --policy; PolicyFlags must report the root's value, not the
// leaf's (a file path, for kit's uri leaves).
func TestPolicyFlags_LeafLocalPolicyDoesNotShadowDelegation(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String(policyFlag, "", "")
	root.PersistentFlags().Int(maxOpsFlag, 0, "")

	leaf := &cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}}
	leaf.Flags().String(policyFlag, "", "policy file")
	shadowFree := &cobra.Command{Use: "plain", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(leaf, shadowFree)

	root.SetArgs([]string{"leaf", "--policy", "/tmp/file.yaml", "--max-ops", "3"})
	require.NoError(t, root.Execute())
	name, maxOps := PolicyFlags(leaf)
	assert.Empty(t, name, "leaf-local --policy is a file path, not a delegation policy name")
	assert.Equal(t, 3, maxOps)

	root.SetArgs([]string{"plain", "--policy", "strict"})
	require.NoError(t, root.Execute())
	name, _ = PolicyFlags(shadowFree)
	assert.Equal(t, "strict", name, "ordinary leaves still read the global --policy")
}
