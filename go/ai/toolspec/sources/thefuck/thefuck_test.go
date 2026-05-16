package thefuck_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/sources/thefuck"
)

const gitPushRule = `def match(command):
    return 'git push' in command.script and 'set-upstream' in command.output

def get_new_command(command):
    return command.output.split('\n')[1].strip()
`

const noSuchFileRule = `def match(command):
    return 'No such file or directory' in command.output

def get_new_command(command):
    return 'mkdir -p {dir} && {cmd}'.format(dir=dir, cmd=command.script)
`

const complexRule = `import re
from difflib import get_close_matches

def match(command):
    patterns = _get_patterns()
    return any(p.search(command.output) for p in patterns)

def get_new_command(command):
    matches = get_close_matches(cmd, possibilities)
    return matches[0] if matches else None
`

func TestParseRule_GitPush(t *testing.T) {
	ep, err := thefuck.ParseRule("git_push", gitPushRule)
	require.NoError(t, err)
	require.NotNil(t, ep)

	assert.Equal(t, "git push && set-upstream", ep.Pattern)
	assert.Equal(t, `command.output.split('\n')[1].strip()`, ep.Fix)
	assert.Equal(t, "thefuck:git_push", ep.Source)
}

func TestParseRule_NoSuchFile(t *testing.T) {
	ep, err := thefuck.ParseRule("no_such_file", noSuchFileRule)
	require.NoError(t, err)
	require.NotNil(t, ep)

	assert.Equal(t, "No such file or directory", ep.Pattern)
	assert.Contains(t, ep.Fix, "mkdir -p")
	assert.Equal(t, "thefuck:no_such_file", ep.Source)
}

func TestParseRule_ComplexSkipped(t *testing.T) {
	ep, err := thefuck.ParseRule("complex", complexRule)
	require.NoError(t, err)
	assert.Nil(t, ep, "complex rules should be skipped")
}

func TestParseRules(t *testing.T) {
	rules := map[string]string{
		"git_push":     gitPushRule,
		"no_such_file": noSuchFileRule,
		"complex":      complexRule,
	}

	ts := thefuck.ParseRules("git", rules)
	require.NotNil(t, ts)
	assert.Equal(t, "git", ts.Name)
	assert.Len(t, ts.ErrorPatterns, 2, "should collect 2 patterns, skip complex")

	// Verify both patterns are present (order is map-iteration-dependent).
	sources := map[string]bool{}
	for _, ep := range ts.ErrorPatterns {
		sources[ep.Source] = true
	}
	assert.True(t, sources["thefuck:git_push"])
	assert.True(t, sources["thefuck:no_such_file"])
}
