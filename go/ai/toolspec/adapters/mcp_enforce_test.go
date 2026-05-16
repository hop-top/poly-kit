package adapters_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
	"hop.top/kit/go/ai/toolspec/policy"
)

// fixtureManifest returns a small manifest with the four canonical
// side-effect classes covered by one leaf each. Tests reach into
// it by path to drive the enforcement gate.
func fixtureManifest() toolspec.Manifest {
	return toolspec.Manifest{
		Tool:          "mytool",
		Version:       "1.0.0",
		SchemaVersion: "1.0",
		Commands: []toolspec.ManifestCommand{
			{Path: []string{"mytool", "list"}, SideEffect: "read", Idempotent: "yes"},
			{Path: []string{"mytool", "create"}, SideEffect: "write", Idempotent: "no"},
			{Path: []string{"mytool", "delete"}, SideEffect: "destructive", Idempotent: "no"},
			{Path: []string{"mytool", "shell"}, SideEffect: "interactive", Idempotent: "no"},
			{Path: []string{"mytool", "weird"}, SideEffect: ""},
		},
	}
}

func TestEnforce_ReadAutoAllow(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "list"}, policy.Default())
	assert.Equal(t, policy.ActionAutoAllow, env.Decision.Action)
	assert.Nil(t, env.Error, "auto-allow does not populate Error")
}

func TestEnforce_WriteAutoAllowNetworkNone(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "create"}, policy.Default())
	// Default network=none + write → auto-allow per the table.
	assert.Equal(t, policy.ActionAutoAllow, env.Decision.Action)
}

func TestEnforce_DestructivePrompts(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "delete"}, policy.Default())
	assert.Equal(t, policy.ActionPrompt, env.Decision.Action)
	assert.Nil(t, env.Error, "prompt does not populate Error")
}

func TestEnforce_InteractivePromptsAllNetworks(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "shell"}, policy.Default())
	assert.Equal(t, policy.ActionPrompt, env.Decision.Action)
}

func TestEnforce_UnknownPathDenies(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "ghost"}, policy.Default())
	assert.Equal(t, policy.ActionDeny, env.Decision.Action)
	require.NotNil(t, env.Error)
	assert.Equal(t, adapters.MCPErrorCodePolicyDeny, env.Error.Code)
	assert.Contains(t, env.Error.Message, "not advertised")
	assert.Equal(t, "command-not-advertised", env.Error.Data["reason"])
}

func TestEnforce_MissingSideEffectFailsSafe(t *testing.T) {
	t.Parallel()
	// SideEffect="" treated as destructive by the gate so unannotated
	// commands fail safe — destructive at network=none prompts.
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "weird"}, policy.Default())
	assert.Equal(t, policy.ActionPrompt, env.Decision.Action,
		"unannotated → destructive → prompt at network=none")
}

func TestEnforce_OverlayPolicyApplies(t *testing.T) {
	t.Parallel()
	overlay, err := policy.LoadBytes([]byte(`
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: deny
    reason: "production lockdown"
`), "production.yaml")
	require.NoError(t, err)

	env := adapters.EnforceMCPRequest(
		fixtureManifest(),
		[]string{"mytool", "list"},
		policy.Merge(policy.Default(), overlay),
	)
	assert.Equal(t, policy.ActionDeny, env.Decision.Action)
	require.NotNil(t, env.Error)
	assert.Equal(t, "production.yaml", env.Decision.Source)
	assert.Equal(t, "policy-deny", env.Error.Data["reason"])
}

func TestEnforce_ReasonFlagsNetworkStub(t *testing.T) {
	t.Parallel()
	env := adapters.EnforceMCPRequest(fixtureManifest(), []string{"mytool", "list"}, policy.Default())
	assert.Contains(t, env.Decision.Reason, "network axis: stub",
		"reason should call out the safety-ladder dependency")
}

func TestEnforce_PathEcho(t *testing.T) {
	t.Parallel()
	path := []string{"mytool", "list"}
	env := adapters.EnforceMCPRequest(fixtureManifest(), path, policy.Default())
	assert.Equal(t, path, env.Path, "path is echoed back for audit logs")
}
