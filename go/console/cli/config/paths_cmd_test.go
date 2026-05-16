package config_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	kitcliconfig "hop.top/kit/go/console/cli/config"
)

// fakeChain returns a deterministic four-rung resolution chain. Only
// the user-scope file claims to exist by default; tests override
// Exists fields as needed.
func fakeChain(cwd string) []kitcliconfig.ResolvedPath {
	return []kitcliconfig.ResolvedPath{
		{Path: filepath.Join(cwd, ".tool.yaml"), Source: "cwd", Scope: "cwd", Exists: false},
		{Path: filepath.Join(cwd, "..", ".tool.yaml"), Source: "project", Scope: "project", Exists: false},
		{Path: "/home/u/.config/tool/config.yaml", Source: "user", Scope: "user", Exists: true},
		{Path: "/etc/tool/config.yaml", Source: "system", Scope: "system", Exists: false},
	}
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

func TestCommand_AttachesPathAndPaths(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Use] = true
	}
	assert.True(t, names["path"], "expected `path` subcommand")
	assert.True(t, names["paths"], "expected `paths` subcommand")
}

func TestPaths_TextDefault(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "paths")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[0], ".tool.yaml")
	assert.Equal(t, "/home/u/.config/tool/config.yaml", lines[2])
}

func TestPaths_JSON(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "paths", "--format", "json")
	require.NoError(t, err)

	var got []kitcliconfig.ResolvedPath
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 4)
	assert.Equal(t, "user", got[2].Source)
	assert.True(t, got[2].Exists)
	assert.False(t, got[0].Exists)
}

func TestPaths_YAML(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "paths", "--format", "yaml")
	require.NoError(t, err)

	var got []kitcliconfig.ResolvedPath
	require.NoError(t, yaml.Unmarshal([]byte(out), &got))
	require.Len(t, got, 4)
	assert.Equal(t, "user", got[2].Source)
}

func TestPath_HighestExisting(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "path")
	require.NoError(t, err)
	assert.Equal(t, "/home/u/.config/tool/config.yaml\n", out)
}

func TestPath_PrefersHigherPrecedence(t *testing.T) {
	resolver := func(string) []kitcliconfig.ResolvedPath {
		return []kitcliconfig.ResolvedPath{
			{Path: "/repo/.tool.yaml", Source: "cwd", Scope: "cwd", Exists: true},
			{Path: "/home/u/.config/tool/config.yaml", Source: "user", Scope: "user", Exists: true},
		}
	}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(resolver))
	out, _, err := runCmd(t, cmd, "path")
	require.NoError(t, err)
	assert.Equal(t, "/repo/.tool.yaml\n", out)
}

func TestPath_NoExistingExits1(t *testing.T) {
	resolver := func(string) []kitcliconfig.ResolvedPath {
		return []kitcliconfig.ResolvedPath{
			{Path: "/etc/tool/config.yaml", Source: "system", Scope: "system", Exists: false},
		}
	}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(resolver))
	_, errOut, err := runCmd(t, cmd, "path")
	require.Error(t, err)
	assert.True(t, kitcliconfig.IsNoConfig(err), "expected IsNoConfig sentinel, got %v", err)
	assert.Contains(t, errOut, "no config file found in resolution chain")
}

func TestPath_EmptyChainExits1(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(func(string) []kitcliconfig.ResolvedPath { return nil }))
	_, _, err := runCmd(t, cmd, "path")
	require.Error(t, err)
	assert.True(t, kitcliconfig.IsNoConfig(err))
}

func TestFromFlag_Override(t *testing.T) {
	var seenCwd string
	resolver := func(cwd string) []kitcliconfig.ResolvedPath {
		seenCwd = cwd
		return []kitcliconfig.ResolvedPath{
			{Path: filepath.Join(cwd, "config.yaml"), Source: "cwd", Scope: "cwd", Exists: true},
		}
	}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(resolver))
	out, _, err := runCmd(t, cmd, "paths", "--from", "/custom/dir")
	require.NoError(t, err)
	assert.Equal(t, "/custom/dir", seenCwd)
	assert.Contains(t, out, "/custom/dir/config.yaml")
}

func TestPath_JSONFormat(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "path", "--format", "json")
	require.NoError(t, err)

	var got kitcliconfig.ResolvedPath
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "user", got.Source)
	assert.Equal(t, "/home/u/.config/tool/config.yaml", got.Path)
	assert.True(t, got.Exists)
}

func TestPath_YAMLFormat(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	out, _, err := runCmd(t, cmd, "path", "--format", "yaml")
	require.NoError(t, err)

	var got kitcliconfig.ResolvedPath
	require.NoError(t, yaml.Unmarshal([]byte(out), &got))
	assert.Equal(t, "user", got.Source)
}

func TestFormat_RejectsUnknown(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(fakeChain))
	_, _, err := runCmd(t, cmd, "paths", "--format", "csv")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "csv")
}

func TestRegisterPathSubcommands_AttachesToHostParent(t *testing.T) {
	parent := &cobra.Command{Use: "config"}
	kitcliconfig.RegisterPathSubcommands(parent, "tool", kitcliconfig.WithResolver(fakeChain))
	require.Len(t, parent.Commands(), 2)

	out, _, err := runCmd(t, parent, "path")
	require.NoError(t, err)
	assert.Contains(t, out, "/home/u/.config/tool/config.yaml")
}

func TestPaths_EmptyChainPrintsEmptyJSONArray(t *testing.T) {
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithResolver(func(string) []kitcliconfig.ResolvedPath { return nil }))
	out, _, err := runCmd(t, cmd, "paths", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", strings.TrimSpace(out)+"\n")
}

func TestStandaloneSubcommands(t *testing.T) {
	pathCmd := kitcliconfig.PathCommand("tool", kitcliconfig.WithResolver(fakeChain))
	assert.Equal(t, "path", pathCmd.Use)
	pathsCmd := kitcliconfig.PathsCommand("tool", kitcliconfig.WithResolver(fakeChain))
	assert.Equal(t, "paths", pathsCmd.Use)
}

func TestDefaultResolver_NoOp(t *testing.T) {
	// No resolver supplied: defaults to nil, paths prints nothing,
	// path exits 1 with the sentinel.
	cmd := kitcliconfig.Command("tool")
	out, _, err := runCmd(t, cmd, "paths")
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(out))

	cmd2 := kitcliconfig.Command("tool")
	_, _, err = runCmd(t, cmd2, "path")
	require.Error(t, err)
	assert.True(t, kitcliconfig.IsNoConfig(err))
}
