// integration_test.go covers the full ADR-0019 path:
//
//	cobra tree → BuildManifest → JSON round-trip → EnforceMCPRequest
//
// Per task T-0499 (kit-toolspec-ai-harness-contract). The test
// builds a fixture kit Root, walks it into a Manifest, marshals
// the Manifest through JSON to validate the wire format, parses
// it back, and runs the resolved Manifest through the policy
// gate for every (side_effect, network) cell the default table
// covers. Drift in the manifest schema OR the policy table OR the
// gate surfaces here as a failed assertion.

package adapters_test

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	"hop.top/kit/go/ai/toolspec/policy"
	kitcli "hop.top/kit/go/console/cli"
)

func TestIntegration_FullPath(t *testing.T) {
	t.Parallel()

	// 1. Build the fixture cobra tree with one leaf per side-effect class.
	r := kitcli.New(kitcli.Config{
		Name:    "myc",
		Version: "0.0.1",
		Short:   "fixture for integration test",
	})
	for _, leaf := range []struct {
		name string
		se   kitcli.SideEffect
	}{
		{"list", kitcli.SideEffectRead},
		{"create", kitcli.SideEffectWrite},
		{"delete", kitcli.SideEffectDestructive},
		{"shell", kitcli.SideEffectInteractive},
	} {
		c := &cobra.Command{
			Use:   leaf.name,
			Short: leaf.name + " a thing",
			RunE:  func(*cobra.Command, []string) error { return nil },
		}
		kitcli.SetSideEffect(c, leaf.se)
		kitcli.SetIdempotency(c, kitcli.IdempotencyYes)
		r.Cmd.AddCommand(c)
	}

	// 2. Walk into a Manifest.
	manifest := speccli.EmitManifest(r, "1.0")
	require.Equal(t, "myc", manifest.Tool)
	require.Equal(t, "1.0", manifest.SchemaVersion)
	require.Len(t, manifest.Commands, 4, "every fixture leaf surfaces")

	// 3. Round-trip through JSON.
	enc, err := json.Marshal(manifest)
	require.NoError(t, err)
	var roundTripped toolspec.Manifest
	require.NoError(t, json.Unmarshal(enc, &roundTripped))
	assert.Equal(t, manifest, roundTripped, "JSON round-trip is lossless")

	// 4. Run every leaf through the policy gate. Defaults today:
	//    network=none for every command (network-axis stub). So:
	//      read     → auto-allow
	//      write    → auto-allow
	//      destructive → prompt
	//      interactive → prompt
	cases := map[string]policy.Action{
		"list":   policy.ActionAutoAllow,
		"create": policy.ActionAutoAllow,
		"delete": policy.ActionPrompt,
		"shell":  policy.ActionPrompt,
	}
	for leaf, want := range cases {
		t.Run(leaf, func(t *testing.T) {
			env := adapters.EnforceMCPRequest(
				roundTripped,
				[]string{"myc", leaf},
				policy.Default(),
			)
			assert.Equal(t, want, env.Decision.Action,
				"leaf %s resolved differently than expected", leaf)
			if want == policy.ActionDeny {
				require.NotNil(t, env.Error)
			} else {
				assert.Nil(t, env.Error,
					"only deny populates Error envelope")
			}
		})
	}
}

func TestIntegration_OverlayDeniesAcrossTheBoard(t *testing.T) {
	t.Parallel()
	r := kitcli.New(kitcli.Config{Name: "myc", Version: "0.0.1", Short: "fixture"})
	for _, name := range []string{"list", "create"} {
		c := &cobra.Command{
			Use:  name,
			RunE: func(*cobra.Command, []string) error { return nil },
		}
		kitcli.SetSideEffect(c, kitcli.SideEffectRead)
		kitcli.SetIdempotency(c, kitcli.IdempotencyYes)
		r.Cmd.AddCommand(c)
	}
	m := speccli.EmitManifest(r, "1.0")

	overlay, err := policy.LoadBytes([]byte(`
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: deny
    reason: "lockdown"
`), "lockdown.yaml")
	require.NoError(t, err)

	merged := policy.Merge(policy.Default(), overlay)

	for _, name := range []string{"list", "create"} {
		env := adapters.EnforceMCPRequest(m, []string{"myc", name}, merged)
		// Note: only "list" is read; create is also read in this fixture.
		assert.Equal(t, policy.ActionDeny, env.Decision.Action)
		require.NotNil(t, env.Error)
		assert.Equal(t, adapters.MCPErrorCodePolicyDeny, env.Error.Code)
		assert.Contains(t, env.Decision.Reason, "lockdown")
	}
}
