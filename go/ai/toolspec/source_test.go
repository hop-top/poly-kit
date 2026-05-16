package toolspec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceFunc(t *testing.T) {
	called := false
	fn := SourceFunc(func(tool string) (*ToolSpec, error) {
		called = true
		assert.Equal(t, "git", tool)
		return &ToolSpec{Name: "git"}, nil
	})

	spec, err := fn.Resolve("git")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "git", spec.Name)
}

func TestChainSources(t *testing.T) {
	first := SourceFunc(func(_ string) (*ToolSpec, error) {
		return &ToolSpec{
			Name:     "git",
			Commands: []Command{{Name: "status"}},
		}, nil
	})
	second := SourceFunc(func(_ string) (*ToolSpec, error) {
		return &ToolSpec{
			Name:     "gh",
			Commands: []Command{{Name: "pr"}},
			Flags:    []Flag{{Name: "--json"}},
			ErrorPatterns: []ErrorPattern{
				{Pattern: "not a repo", Fix: "git init"},
			},
		}, nil
	})

	chain := ChainSources(first, second)
	spec, err := chain.Resolve("git")
	require.NoError(t, err)

	assert.Equal(t, "git", spec.Name, "first source name wins")
	assert.Len(t, spec.Commands, 1, "first source commands win")
	assert.Equal(t, "status", spec.Commands[0].Name)
	assert.Len(t, spec.Flags, 1, "flags filled from second source")
	assert.Equal(t, "--json", spec.Flags[0].Name)
	assert.Len(t, spec.ErrorPatterns, 1, "error patterns filled from second")
}
