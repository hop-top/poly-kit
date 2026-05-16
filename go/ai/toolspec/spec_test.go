package toolspec_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/toolspec"
)

func TestToolSpec_JSONRoundTrip(t *testing.T) {
	orig := toolspec.ToolSpec{
		Name: "git",
		Commands: []toolspec.Command{
			{
				Name:    "commit",
				Aliases: []string{"ci"},
				Flags: []toolspec.Flag{
					{Name: "--message", Short: "-m", Type: "string", Description: "commit message"},
				},
			},
		},
		Flags: []toolspec.Flag{
			{Name: "--version", Short: "-v", Type: "bool"},
		},
		ErrorPatterns: []toolspec.ErrorPattern{
			{Pattern: "not a git repository", Fix: "git init", Source: "thefuck"},
		},
		Workflows: []toolspec.Workflow{
			{
				Name:  "basic commit",
				Steps: []string{"git add .", "git commit -m 'msg'"},
				After: map[string][]string{"git add": {"git commit"}},
			},
		},
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded toolspec.ToolSpec
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, orig, decoded)
}

func TestToolSpec_FindCommand(t *testing.T) {
	ts := toolspec.ToolSpec{
		Name: "git",
		Commands: []toolspec.Command{
			{
				Name: "remote",
				Children: []toolspec.Command{
					{Name: "add"},
					{Name: "remove"},
				},
			},
			{Name: "commit"},
		},
	}

	t.Run("top-level", func(t *testing.T) {
		c := ts.FindCommand("commit")
		require.NotNil(t, c)
		assert.Equal(t, "commit", c.Name)
	})

	t.Run("nested", func(t *testing.T) {
		c := ts.FindCommand("add")
		require.NotNil(t, c)
		assert.Equal(t, "add", c.Name)
	})

	t.Run("not found", func(t *testing.T) {
		assert.Nil(t, ts.FindCommand("nonexistent"))
	})
}

func TestToolSpec_JSONRoundTrip_NewFields(t *testing.T) {
	orig := toolspec.ToolSpec{
		Name:          "myctl",
		SchemaVersion: "1.0",
		StateIntrospection: &toolspec.StateIntrospection{
			ConfigCommands: []string{"config show"},
			EnvVars:        []string{"MYCTL_TOKEN"},
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
				Format:  "json",
				Fields:  []string{"status", "url"},
				Example: `{"status":"ok"}`,
			},
			Deprecated:      true,
			DeprecatedSince: "2.0",
			ReplacedBy:      "rollout",
			Intent: &toolspec.Intent{
				Domain:   "deployment",
				Category: "infrastructure",
				Tags:     []string{"prod"},
			},
			SuggestedNext: []string{"status", "rollback"},
			Flags: []toolspec.Flag{{
				Name:       "--legacy",
				Deprecated: true,
				ReplacedBy: "--modern",
			}},
		}},
		ErrorPatterns: []toolspec.ErrorPattern{{
			Pattern:    "deploy failed",
			Fix:        "check logs",
			Source:     "manual",
			Cause:      "bad_state",
			Fixes:      []string{"check logs", "rollback"},
			Confidence: 0.9,
			Provenance: &toolspec.Provenance{
				Source: "manual", RetrievedAt: "2026-01-01", Confidence: 1.0,
			},
		}},
		Workflows: []toolspec.Workflow{{
			Name:  "deploy flow",
			Steps: []string{"build", "test", "deploy"},
			Provenance: &toolspec.Provenance{
				Source: "llm", Confidence: 0.6,
			},
		}},
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded toolspec.ToolSpec
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, orig, decoded)

	// Spot-check pointer fields survived round-trip.
	require.NotNil(t, decoded.StateIntrospection)
	assert.Equal(t, []string{"MYCTL_TOKEN"}, decoded.StateIntrospection.EnvVars)

	require.NotNil(t, decoded.Commands[0].Contract)
	assert.True(t, decoded.Commands[0].Contract.Retryable)

	require.NotNil(t, decoded.Commands[0].Safety)
	assert.Equal(t, toolspec.SafetyLevelDangerous, decoded.Commands[0].Safety.Level)

	require.NotNil(t, decoded.Commands[0].Intent)
	assert.Equal(t, "deployment", decoded.Commands[0].Intent.Domain)

	require.NotNil(t, decoded.ErrorPatterns[0].Provenance)
	assert.Equal(t, float32(1.0), decoded.ErrorPatterns[0].Provenance.Confidence)

	require.NotNil(t, decoded.Workflows[0].Provenance)
	assert.Equal(t, "llm", decoded.Workflows[0].Provenance.Source)

	// Flag deprecation fields
	assert.True(t, decoded.Commands[0].Flags[0].Deprecated)
	assert.Equal(t, "--modern", decoded.Commands[0].Flags[0].ReplacedBy)
}

func TestToolSpec_FindCommand_PrefersShallow(t *testing.T) {
	ts := toolspec.ToolSpec{
		Name: "mycli",
		Commands: []toolspec.Command{
			{
				Name: "prompt",
				Children: []toolspec.Command{
					{Name: "task"}, // nested: prompt/task
				},
			},
			{Name: "task"}, // top-level
		},
	}

	t.Run("top-level wins over nested", func(t *testing.T) {
		c := ts.FindCommand("task")
		require.NotNil(t, c)
		assert.Equal(t, "task", c.Name)
		// Ensure we got the top-level command, not the nested child.
		// The top-level "task" lives at ts.Commands[1]; the nested one
		// is at ts.Commands[0].Children[0]. Pointer comparison confirms.
		assert.Same(t, &ts.Commands[1], c)
	})

	t.Run("nested still found when no top-level match", func(t *testing.T) {
		ts2 := toolspec.ToolSpec{
			Name: "mycli",
			Commands: []toolspec.Command{
				{
					Name: "prompt",
					Children: []toolspec.Command{
						{Name: "generate"},
					},
				},
			},
		}
		c := ts2.FindCommand("generate")
		require.NotNil(t, c)
		assert.Equal(t, "generate", c.Name)
	})
}
