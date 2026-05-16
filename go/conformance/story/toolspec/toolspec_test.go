package toolspec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/story/toolspec"
)

const sampleSpec = `name: spaced
schema_version: "1"
commands:
  - name: launch
    flags:
      - name: --payload
        type: string
      - name: --output
        short: -o
        type: string
      - name: --dry-run
        type: bool
  - name: mission
    children:
      - name: list
  - name: config
    children:
      - name: show
        flags:
          - name: --format
            type: string
`

func writeSpec(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool.toolspec.yaml")
	require.NoError(t, os.WriteFile(p, []byte(sampleSpec), 0644))
	return p
}

func TestLoadFromPath(t *testing.T) {
	p := writeSpec(t)
	ts, err := toolspec.LoadFromPath(p)
	require.NoError(t, err)
	assert.Equal(t, "spaced", ts.Name)
	require.Len(t, ts.Commands, 3)
}

func TestResolveCommandLeaf(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	cmd, ok := ts.ResolveCommand([]string{"launch"})
	require.True(t, ok)
	assert.Equal(t, "launch", cmd.Name)
}

func TestResolveCommandNested(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	cmd, ok := ts.ResolveCommand([]string{"mission", "list"})
	require.True(t, ok)
	assert.Equal(t, "list", cmd.Name)
}

func TestResolveCommandUnknown(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	_, ok := ts.ResolveCommand([]string{"nonexistent"})
	assert.False(t, ok)
}

func TestResolveCommandSkipsFlags(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	cmd, ok := ts.ResolveCommand([]string{"launch", "--dry-run", "--payload", "alpha"})
	require.True(t, ok)
	assert.Equal(t, "launch", cmd.Name)
}

func TestResolveFlagDeclared(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	cmd, _ := ts.ResolveCommand([]string{"launch"})
	assert.True(t, toolspec.ResolveFlag(cmd, "--payload"))
	assert.True(t, toolspec.ResolveFlag(cmd, "--payload=alpha"))
	assert.True(t, toolspec.ResolveFlag(cmd, "-o"), "short form should resolve")
}

func TestResolveFlagGlobal(t *testing.T) {
	assert.True(t, toolspec.ResolveFlag(nil, "--help"))
	assert.True(t, toolspec.ResolveFlag(nil, "-h"))
	assert.True(t, toolspec.ResolveFlag(nil, "--format"))
}

func TestResolveFlagUnknown(t *testing.T) {
	ts, err := toolspec.LoadFromPath(writeSpec(t))
	require.NoError(t, err)
	cmd, _ := ts.ResolveCommand([]string{"launch"})
	assert.False(t, toolspec.ResolveFlag(cmd, "--bogus"))
}
