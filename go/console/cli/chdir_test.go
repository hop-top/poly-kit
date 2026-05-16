package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// runChdirHook invokes the root's PersistentPreRunE after parsing
// args via cobra. Returns the hook error.
func runChdirHook(t *testing.T, r *cli.Root, args []string) error {
	t.Helper()
	require.NotNil(t, r.Cmd.PersistentPreRunE, "expected chdir hook on root")
	require.NoError(t, r.Cmd.ParseFlags(args))
	return r.Cmd.PersistentPreRunE(r.Cmd, nil)
}

func TestChdir_TargetIsDir(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "t", DisableValidate: true})
	require.NoError(t, runChdirHook(t, r, []string{"--chdir", dir}))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	// Resolve symlinks (macOS /tmp → /private/tmp).
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal)
}

func TestChdir_ResolverResolvesMissing(t *testing.T) {
	resolved := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{
		Name: "t", Version: "0.0.1", Short: "t",
		ChdirResolver: func(target string) (string, error) {
			assert.Equal(t, "shortname", target)
			return resolved, nil
		},
		DisableValidate: true,
	})
	require.NoError(t, runChdirHook(t, r, []string{"-C", "shortname"}))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	wantReal, _ := filepath.EvalSymlinks(resolved)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal)
}

func TestChdir_ResolverError(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	resolverErr := errors.New("not found in registry")
	r := cli.New(cli.Config{
		Name: "t", Version: "0.0.1", Short: "t",
		ChdirResolver:   func(string) (string, error) { return "", resolverErr },
		DisableValidate: true,
	})

	err = runChdirHook(t, r, []string{"-C", "bogus"})
	require.Error(t, err)
	assert.ErrorIs(t, err, resolverErr)
}

func TestChdir_MissingNoResolverErrors(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "t", DisableValidate: true})
	err = runChdirHook(t, r, []string{"-C", "/nope/does-not-exist-xyz"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"/nope/does-not-exist-xyz"`)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestChdir_EmptyTargetNoOp(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "t", DisableValidate: true})
	require.NoError(t, runChdirHook(t, r, nil))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, orig, cwd)
}

func TestChdir_DisabledOmitsFlag(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "t", Version: "0.0.1", Short: "t",
		Disable:         cli.Disable{Chdir: true},
		DisableValidate: true,
	})
	assert.Nil(t, r.Cmd.PersistentFlags().Lookup("chdir"),
		"--chdir flag must be absent when Disable.Chdir is set")
	assert.Nil(t, r.Cmd.PersistentFlags().ShorthandLookup("C"),
		"-C shorthand must be absent when Disable.Chdir is set")
}
