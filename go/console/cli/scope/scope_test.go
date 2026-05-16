package scope_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scopecmd "hop.top/kit/go/console/cli/scope"
	scopepkg "hop.top/kit/go/core/scope"
)

// resetViper resets the global viper format key so tests don't leak.
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := scopecmd.Cmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestShow_DefaultPolicy_Table(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())

	// Override Default with a known small policy for deterministic output.
	restore := scopepkg.SetDefault(scopepkg.New().
		SetMode(scopepkg.Strict).
		Allow("/tmp/allowed/**").
		Deny("/tmp/denied/**"))
	t.Cleanup(restore)

	out, err := runCmd(t, "show")
	require.NoError(t, err)
	assert.Contains(t, out, "MODE: strict")
	assert.Contains(t, out, "ALLOW")
	assert.Contains(t, out, "/tmp/allowed/**")
	assert.Contains(t, out, "DENY")
	assert.Contains(t, out, "/tmp/denied/**")
}

func TestShow_JSON(t *testing.T) {
	resetViper(t)
	viper.Set("format", "json")
	restore := scopepkg.SetDefault(scopepkg.New().Allow("/tmp/x/**"))
	t.Cleanup(restore)

	out, err := runCmd(t, "show")
	require.NoError(t, err)
	assert.Contains(t, out, `"mode"`)
	assert.Contains(t, out, `"rules"`)
	assert.Contains(t, out, "/tmp/x/**")
}

func TestShow_FromTool(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	toolDir := filepath.Join(dir, "mytool")
	require.NoError(t, os.MkdirAll(toolDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(toolDir, "scope.yaml"),
		[]byte("mode: warn\nallow:\n  - \"/tmp/x/**\"\n"),
		0o644,
	))

	out, err := runCmd(t, "show", "--tool", "mytool")
	require.NoError(t, err)
	assert.Contains(t, out, "MODE: warn")
	assert.Contains(t, out, "tool=mytool")
	assert.Contains(t, out, "/tmp/x/**")
}

func TestCheck_Allowed(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().Allow("/tmp/ok/**"))
	t.Cleanup(restore)

	out, err := runCmd(t, "check", "/tmp/ok/file")
	require.NoError(t, err)
	assert.Contains(t, out, "allowed")
}

func TestCheck_Denied_ExitsWithDeniedSentinel(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().Deny("/tmp/no/**"))
	t.Cleanup(restore)

	out, err := runCmd(t, "check", "/tmp/no/secret")
	require.Error(t, err)
	assert.True(t, scopecmd.IsDeniedExit(err))
	assert.Contains(t, out, "denied")
}

func TestCheck_BadOpIsUsageError(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New())
	t.Cleanup(restore)

	_, err := runCmd(t, "check", "/tmp/x", "--op", "fly")
	require.Error(t, err)
	assert.True(t, scopecmd.IsUsageError(err))
}

func TestTest_AllAllowed(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().Allow("/tmp/**"))
	t.Cleanup(restore)

	out, err := runCmd(t, "test", "/tmp/a", "/tmp/b", "/tmp/c")
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(out, "\n"), "header + 3 rows")
	assert.NotContains(t, out, "denied")
}

func TestTest_AnyDeniedExits(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().
		Allow("/tmp/ok/**").
		Deny("/tmp/no/**"))
	t.Cleanup(restore)

	_, err := runCmd(t, "test", "/tmp/ok/x", "/tmp/no/y", "/tmp/ok/z")
	require.Error(t, err)
	assert.True(t, scopecmd.IsDeniedExit(err))
}

func TestExitClassifiers(t *testing.T) {
	assert.True(t, scopecmd.IsDeniedExit(deniedSentinel(t)))
	assert.False(t, scopecmd.IsDeniedExit(errors.New("plain")))
	assert.False(t, scopecmd.IsUsageError(errors.New("plain")))
}

// deniedSentinel constructs a denied-exit error via a small `check` run.
func deniedSentinel(t *testing.T) error {
	t.Helper()
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().Deny("/x/**"))
	t.Cleanup(restore)
	_, err := runCmd(t, "check", "/x/y")
	return err
}

// TestCheck_OutputToFile_JSON exercises the Dispatch migration on
// `scope check`: --format json and --output write to a temp file via
// the global viper (matching how scope reads format in production
// when wired under cli.Root). Regression guard for the T-0990
// callsite swap.
func TestCheck_OutputToFile_JSON(t *testing.T) {
	resetViper(t)
	t.Setenv("HOME", t.TempDir())
	restore := scopepkg.SetDefault(scopepkg.New().Allow("/tmp/ok/**"))
	t.Cleanup(restore)

	out := filepath.Join(t.TempDir(), "check.json")
	viper.Set("format", "json")
	viper.Set("output", out)
	defer viper.Reset()

	_, err := runCmd(t, "check", "/tmp/ok/file")
	require.NoError(t, err)

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, `"path"`)
	assert.Contains(t, s, `"allowed"`)
}
