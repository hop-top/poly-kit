package completion_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/sources/completion"
)

const zshFixture = `#compdef git

_git() {
  local -a commands
  commands=(
    'add:Add file contents to the index'
    'commit:Record changes to the repository'
    'push:Update remote refs'
    'pull:Fetch and integrate with another repository'
  )
  _describe 'command' commands

  _arguments \
    '(-v --verbose)'{-v,--verbose}'[Be verbose]' \
    '--dry-run[Show what would be done]'
}
`

const bashFixture = `_git_completion() {
  local opts="add commit push pull status log diff"
  COMPREPLY=( $(compgen -W "$opts" -- "${COMP_WORDS[COMP_CWORD]}") )
}
complete -F _git_completion git
`

func TestParseZshCompletion(t *testing.T) {
	ts := completion.ParseZshCompletion("git", zshFixture)
	require.NotNil(t, ts)
	assert.Equal(t, "git", ts.Name)

	// Commands
	assert.Len(t, ts.Commands, 4)
	names := make([]string, len(ts.Commands))
	for i, c := range ts.Commands {
		names[i] = c.Name
	}
	assert.Equal(t, []string{"add", "commit", "push", "pull"}, names)

	// Flags
	assert.Len(t, ts.Flags, 2)

	assert.Equal(t, "--verbose", ts.Flags[0].Name)
	assert.Equal(t, "-v", ts.Flags[0].Short)
	assert.Equal(t, "Be verbose", ts.Flags[0].Description)

	assert.Equal(t, "--dry-run", ts.Flags[1].Name)
	assert.Empty(t, ts.Flags[1].Short)
	assert.Equal(t, "Show what would be done", ts.Flags[1].Description)
}

func TestParseBashCompletion(t *testing.T) {
	ts := completion.ParseBashCompletion("git", bashFixture)
	require.NotNil(t, ts)
	assert.Equal(t, "git", ts.Name)

	assert.Len(t, ts.Commands, 7)
	names := make([]string, len(ts.Commands))
	for i, c := range ts.Commands {
		names[i] = c.Name
	}
	assert.Equal(t, []string{"add", "commit", "push", "pull", "status", "log", "diff"}, names)
}

func TestParseZshCompletion_Empty(t *testing.T) {
	ts := completion.ParseZshCompletion("empty", "")
	require.NotNil(t, ts)
	assert.Equal(t, "empty", ts.Name)
	assert.Empty(t, ts.Commands)
	assert.Empty(t, ts.Flags)
}
