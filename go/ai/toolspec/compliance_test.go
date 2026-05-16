package toolspec_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

func TestCompliance_AllFactorsCovered(t *testing.T) {
	// Fully-populated ToolSpec exercising every 12-factor field.
	ts := toolspec.ToolSpec{
		Name:          "example",
		SchemaVersion: "1.0",
		StateIntrospection: &toolspec.StateIntrospection{
			ConfigCommands: []string{"config show"},
			EnvVars:        []string{"EXAMPLE_TOKEN"},
			AuthCommands:   []string{"auth login"},
		},
		Commands: []toolspec.Command{{
			Name: "deploy",
			Contract: &toolspec.Contract{
				Idempotent:  false,
				SideEffects: []string{"deploys to production"},
				Retryable:   true,
			},
			Safety: &toolspec.Safety{
				Level:                toolspec.SafetyLevelDangerous,
				RequiresConfirmation: true,
			},
			PreviewModes: []string{"dryrun", "plan"},
			OutputSchema: &toolspec.OutputSchema{
				Format: "json",
			},
			Intent: &toolspec.Intent{
				Domain:   "deployment",
				Category: "infrastructure",
				Tags:     []string{"prod", "release"},
			},
			SuggestedNext: []string{"status", "rollback"},
		}},
		ErrorPatterns: []toolspec.ErrorPattern{{
			Pattern:    "deployment failed",
			Fix:        "check logs",
			Source:     "manual",
			Cause:      "bad_state",
			Fixes:      []string{"check logs", "rollback"},
			Confidence: 0.9,
			Provenance: &toolspec.Provenance{
				Source:      "manual",
				RetrievedAt: "2026-01-01T00:00:00Z",
				Confidence:  1.0,
			},
		}},
		Workflows: []toolspec.Workflow{{
			Name:  "deploy flow",
			Steps: []string{"build", "test", "deploy"},
			Provenance: &toolspec.Provenance{
				Source:     "llm",
				Confidence: 0.6,
			},
		}},
	}

	// Factor 1: Discovery
	assert.NotEmpty(t, ts.Commands, "F1: commands discovered")
	assert.Equal(t, "deploy", ts.Commands[0].Name)

	// Factor 2: Intent
	require.NotNil(t, ts.Commands[0].Intent, "F2: intent present")
	assert.NotEmpty(t, ts.Commands[0].Intent.Domain)

	// Factor 3: Structured I/O
	require.NotNil(t, ts.Commands[0].OutputSchema, "F3: output schema")
	assert.Equal(t, "json", ts.Commands[0].OutputSchema.Format)

	// Factor 4: Corrective Error Model
	assert.NotEmpty(t, ts.ErrorPatterns[0].Cause, "F4: cause classified")
	assert.NotEmpty(t, ts.ErrorPatterns[0].Fixes, "F4: multiple fixes")
	assert.Greater(t, ts.ErrorPatterns[0].Confidence, float32(0),
		"F4: confidence")

	// Factor 5+7: Contracts
	require.NotNil(t, ts.Commands[0].Contract, "F5+7: contract")
	assert.NotEmpty(t, ts.Commands[0].Contract.SideEffects)

	// Factor 6: Previewability
	assert.NotEmpty(t, ts.Commands[0].PreviewModes, "F6: preview modes")

	// Factor 8: State Transparency
	require.NotNil(t, ts.StateIntrospection, "F8: state introspection")
	assert.NotEmpty(t, ts.StateIntrospection.EnvVars)

	// Factor 9: Guidance
	assert.NotEmpty(t, ts.Commands[0].SuggestedNext, "F9: suggested next")

	// Factor 10: Delegation Safety
	require.NotNil(t, ts.Commands[0].Safety, "F10: safety")
	assert.Equal(t, toolspec.SafetyLevelDangerous,
		ts.Commands[0].Safety.Level)

	// Factor 11: Provenance
	require.NotNil(t, ts.ErrorPatterns[0].Provenance,
		"F11: error provenance")
	require.NotNil(t, ts.Workflows[0].Provenance,
		"F11: workflow provenance")

	// Factor 12: Evolution
	assert.NotEmpty(t, ts.SchemaVersion, "F12: schema version")
}
