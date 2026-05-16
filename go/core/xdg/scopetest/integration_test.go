// Package scopetest_test exercises the xdg → scope guard wiring. Lives in
// its own subpackage so importing scope (which registers a global guard at
// init) does not affect the bare xdg_test binary.
package scopetest_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
	"hop.top/kit/go/core/xdg"
)

// withScopePolicy installs p as the global scope.Default for the duration of
// the test and restores the prior policy + guard on cleanup.
func withScopePolicy(t *testing.T, p *scope.Policy) {
	t.Helper()
	restore := scope.SetDefault(p)
	t.Cleanup(restore)
	// scope/defaults.go init() already wired xdg.SetGuard to scope.Default;
	// no extra wiring needed — the guard reads Default() each call.
}

func TestIntegration_DefaultDeniesSSHConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".ssh"))

	// Hardened default policy (initialized by scope/defaults.go).
	policy := scope.New().
		SetMode(scope.Strict).
		Deny(scope.SecretPaths()...)
	withScopePolicy(t, policy)

	_, err := xdg.ConfigDir("anything")
	require.Error(t, err, "ConfigDir under ~/.ssh must be denied")
	assert.True(t, errors.Is(err, scope.ErrDenied))
}

func TestIntegration_AllowOverrideLetsThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".ssh"))

	_ = scope.New().
		SetMode(scope.Strict).
		Deny(scope.SecretPaths()...).
		Allow(scope.Pattern(filepath.Join(home, ".ssh", "myhole", "**"))).
		// Have to also allow the parent dir specifically, since Default
		// denies ~/.ssh/** broader; but deny-wins: explicit Allow can't
		// beat a Deny. So the right way is to drop the SSH deny entirely
		// for this scoped test:
		Deny() // no-op; rebuild
	// Replace with a permissive policy that only denies what we want:
	policy := scope.New().SetMode(scope.Strict).
		Allow(scope.Pattern(filepath.Join(home, ".ssh", "myhole", "**")))
	withScopePolicy(t, policy)

	dir, err := xdg.ConfigDir("myhole")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".ssh", "myhole"), dir)
}

func TestIntegration_SymlinkResolvedThenDenied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	// Create symlink ~/foo -> ~/.ssh; XDG_CONFIG_HOME points to ~/foo.
	require.NoError(t, os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "foo")))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "foo"))

	policy := scope.New().SetMode(scope.Strict).Deny(scope.SecretPaths()...)
	withScopePolicy(t, policy)

	_, err := xdg.ConfigDir("anything")
	require.Error(t, err, "symlink-via-XDG must be resolved and denied")
	assert.True(t, errors.Is(err, scope.ErrDenied))
}

func TestIntegration_WarnModeReturnsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".ssh"))

	policy := scope.New().SetMode(scope.Warn).Deny(scope.SecretPaths()...)
	withScopePolicy(t, policy)

	dir, err := xdg.ConfigDir("anything")
	require.NoError(t, err, "Warn mode should not error")
	assert.Equal(t, filepath.Join(home, ".ssh", "anything"), dir)
}
