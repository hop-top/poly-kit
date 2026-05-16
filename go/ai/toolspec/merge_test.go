package toolspec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMerge_FillsEmptyCommands(t *testing.T) {
	base := &ToolSpec{Name: "git"}
	overlay := &ToolSpec{
		Commands: []Command{{Name: "commit"}, {Name: "push"}},
	}

	got := Merge(base, overlay)

	assert.Equal(t, "git", got.Name)
	assert.Len(t, got.Commands, 2)
	assert.Equal(t, "commit", got.Commands[0].Name)
}

func TestMerge_DoesNotOverwriteExisting(t *testing.T) {
	base := &ToolSpec{
		Name:     "git",
		Commands: []Command{{Name: "status"}},
		Flags:    []Flag{{Name: "--verbose"}},
	}
	overlay := &ToolSpec{
		Name:     "gh",
		Commands: []Command{{Name: "commit"}, {Name: "push"}},
		Flags:    []Flag{{Name: "--json"}},
		ErrorPatterns: []ErrorPattern{
			{Pattern: "not a repo", Fix: "git init"},
		},
	}

	got := Merge(base, overlay)

	assert.Equal(t, "git", got.Name, "name must not be overwritten")
	assert.Len(t, got.Commands, 1, "commands must not be overwritten")
	assert.Equal(t, "status", got.Commands[0].Name)
	assert.Len(t, got.Flags, 1, "flags must not be overwritten")
	assert.Equal(t, "--verbose", got.Flags[0].Name)
	assert.Len(t, got.ErrorPatterns, 1, "error patterns filled from overlay")
	assert.Equal(t, "not a repo", got.ErrorPatterns[0].Pattern)
}

func TestMerge_FillsSchemaVersion(t *testing.T) {
	base := &ToolSpec{Name: "git"}
	overlay := &ToolSpec{SchemaVersion: "1.0"}

	got := Merge(base, overlay)
	assert.Equal(t, "1.0", got.SchemaVersion, "overlay schema version fills empty base")

	// Non-empty base version is preserved.
	base2 := &ToolSpec{Name: "git", SchemaVersion: "0.9"}
	got2 := Merge(base2, overlay)
	assert.Equal(t, "0.9", got2.SchemaVersion, "base version not overwritten")
}

func TestMerge_FillsStateIntrospection(t *testing.T) {
	base := &ToolSpec{Name: "git"}
	overlay := &ToolSpec{
		StateIntrospection: &StateIntrospection{
			ConfigCommands: []string{"config"},
			EnvVars:        []string{"GIT_DIR"},
		},
	}

	got := Merge(base, overlay)
	require.NotNil(t, got.StateIntrospection)
	assert.Equal(t, []string{"config"}, got.StateIntrospection.ConfigCommands)
	assert.Equal(t, []string{"GIT_DIR"}, got.StateIntrospection.EnvVars)

	// Base with existing StateIntrospection is not overwritten.
	base2 := &ToolSpec{
		Name: "git",
		StateIntrospection: &StateIntrospection{
			EnvVars: []string{"GIT_AUTHOR_NAME"},
		},
	}
	got2 := Merge(base2, overlay)
	require.NotNil(t, got2.StateIntrospection)
	assert.Equal(t, []string{"GIT_AUTHOR_NAME"}, got2.StateIntrospection.EnvVars)
	assert.Nil(t, got2.StateIntrospection.ConfigCommands,
		"base SI preserved; overlay not merged in")
}

func TestMerge_PreservesNewCommandFields(t *testing.T) {
	base := &ToolSpec{
		Name: "myctl",
		Commands: []Command{{
			Name: "deploy",
			Contract: &Contract{
				Idempotent:  false,
				SideEffects: []string{"deploys"},
				Retryable:   true,
			},
			Safety: &Safety{
				Level:                SafetyLevelDangerous,
				RequiresConfirmation: true,
			},
			PreviewModes: []string{"dryrun"},
		}},
	}
	overlay := &ToolSpec{Commands: []Command{{Name: "other"}}}

	got := Merge(base, overlay)
	require.Len(t, got.Commands, 1, "base commands preserved")

	cmd := got.Commands[0]
	require.NotNil(t, cmd.Contract, "Contract survives copy")
	assert.Equal(t, []string{"deploys"}, cmd.Contract.SideEffects)
	assert.True(t, cmd.Contract.Retryable)

	require.NotNil(t, cmd.Safety, "Safety survives copy")
	assert.Equal(t, SafetyLevelDangerous, cmd.Safety.Level)
	assert.True(t, cmd.Safety.RequiresConfirmation)

	assert.Equal(t, []string{"dryrun"}, cmd.PreviewModes,
		"PreviewModes survive copy")
}

func TestDiff_ReportsStateIntrospection(t *testing.T) {
	a := &ToolSpec{Name: "git"}
	b := &ToolSpec{
		Name: "git",
		StateIntrospection: &StateIntrospection{
			ConfigCommands: []string{"config"},
			EnvVars:        []string{"GIT_DIR"},
		},
	}

	got := Diff(a, b)
	require.NotNil(t, got.StateIntrospection,
		"diff reports SI when a has none and b does")
	assert.Equal(t, []string{"config"}, got.StateIntrospection.ConfigCommands)
	assert.Equal(t, []string{"GIT_DIR"}, got.StateIntrospection.EnvVars)

	// Both have SI: diff should NOT include it.
	a2 := &ToolSpec{
		Name:               "git",
		StateIntrospection: &StateIntrospection{EnvVars: []string{"X"}},
	}
	got2 := Diff(a2, b)
	assert.Nil(t, got2.StateIntrospection,
		"diff omits SI when both sides have it")
}

func TestDiff_ReportsAddedErrorPatterns(t *testing.T) {
	a := &ToolSpec{
		Name:     "git",
		Commands: []Command{{Name: "status"}},
	}
	b := &ToolSpec{
		Name:     "git",
		Commands: []Command{{Name: "status"}},
		ErrorPatterns: []ErrorPattern{
			{Pattern: "not a repo", Fix: "git init"},
			{Pattern: "conflict", Fix: "resolve manually"},
		},
	}

	got := Diff(a, b)

	assert.Empty(t, got.Name, "name present in both; diff should be empty")
	assert.Nil(t, got.Commands, "commands present in both; diff should be nil")
	assert.Len(t, got.ErrorPatterns, 2)
	assert.Equal(t, "not a repo", got.ErrorPatterns[0].Pattern)
	assert.Equal(t, "conflict", got.ErrorPatterns[1].Pattern)
}
