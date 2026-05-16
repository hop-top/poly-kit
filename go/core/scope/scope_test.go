package scope_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

// withHome rebinds HOME to a temp dir for the test, so "~" expansion is
// hermetic. Restores on cleanup.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestNew_EmptyPolicyIsStrict(t *testing.T) {
	p := scope.New()
	assert.Equal(t, scope.Strict, p.Mode())
	assert.Empty(t, p.Rules())
}

func TestSetMode_Chains(t *testing.T) {
	p := scope.New().SetMode(scope.Warn)
	assert.Equal(t, scope.Warn, p.Mode())
}

func TestAllowAndDeny_AppendRules(t *testing.T) {
	p := scope.New().
		Allow("/tmp/**").
		Deny("/tmp/secrets/**")
	rules := p.Rules()
	require.Len(t, rules, 2)
	assert.True(t, rules[0].Allow)
	assert.False(t, rules[1].Allow)
	assert.Equal(t, scope.Read|scope.Write|scope.Exec, rules[0].Ops)
}

func TestAllowOp_OnlyMatchingOps(t *testing.T) {
	withHome(t)
	p := scope.New().AllowOp(scope.Read, "/tmp/**")
	dec, err := p.Check("/tmp/x", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Allowed, dec)

	dec, err = p.Check("/tmp/x", scope.Write)
	require.NoError(t, err)
	assert.Equal(t, scope.Unknown, dec, "write op shouldn't match a read-only allow rule")
}

func TestCheck_DenyWins(t *testing.T) {
	p := scope.New().
		Allow("/tmp/**").
		Deny("/tmp/secret/**")
	dec, err := p.Check("/tmp/secret/x", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestCheck_NoMatchReturnsUnknown(t *testing.T) {
	p := scope.New().Allow("/etc/**")
	dec, err := p.Check("/tmp/x", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Unknown, dec)
}

func TestEnforce_StrictDeniesUnknown(t *testing.T) {
	p := scope.New() // empty, Strict
	err := p.Enforce("/tmp/x", scope.Read)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scope.ErrDenied))
}

func TestEnforce_WarnAllowsUnknown(t *testing.T) {
	p := scope.New().SetMode(scope.Warn)
	err := p.Enforce("/tmp/x", scope.Read)
	assert.NoError(t, err)
}

func TestEnforce_WarnAllowsExplicitDeny(t *testing.T) {
	p := scope.New().SetMode(scope.Warn).Deny("/tmp/**")
	err := p.Enforce("/tmp/x", scope.Read)
	assert.NoError(t, err, "Warn mode logs but does not error on Denied")
}

func TestEnforce_PromptCallback(t *testing.T) {
	called := 0
	p := scope.New().
		SetMode(scope.Prompt).
		Deny("/tmp/**").
		SetPromptFunc(func(scope.Path, scope.Op) bool {
			called++
			return true
		})
	err := p.Enforce("/tmp/x", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, 1, called)
}

func TestEnforce_PromptCallbackDeny(t *testing.T) {
	p := scope.New().
		SetMode(scope.Prompt).
		Deny("/tmp/**").
		SetPromptFunc(func(scope.Path, scope.Op) bool { return false })
	err := p.Enforce("/tmp/x", scope.Read)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scope.ErrDenied))
}

func TestEnforce_PromptWithoutCallbackDenies(t *testing.T) {
	p := scope.New().SetMode(scope.Prompt).Deny("/tmp/**")
	err := p.Enforce("/tmp/x", scope.Read)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scope.ErrDenied))
}

func TestCheck_TildeExpansion(t *testing.T) {
	home := withHome(t)
	p := scope.New().Deny("~/.ssh/**")
	dec, err := p.Check(scope.Path(filepath.Join(home, ".ssh", "id_rsa")), scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestCheck_SymlinkResolved(t *testing.T) {
	home := withHome(t)
	// Real ssh dir under home.
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("k"), 0o600))
	// Symlink ~/foo -> ~/.ssh
	require.NoError(t, os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "foo")))

	p := scope.New().Deny("~/.ssh/**")
	dec, err := p.Check(scope.Path(filepath.Join(home, "foo", "id_rsa")), scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec, "symlink should be resolved before matching")
}

func TestCheck_NonexistentMatchesByIntent(t *testing.T) {
	home := withHome(t)
	p := scope.New().Deny("~/.ssh/**")
	// Path does not exist on disk yet; deny rule must still match.
	dec, err := p.Check(scope.Path(filepath.Join(home, ".ssh", "future_key")), scope.Write)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestNewPath_Errors(t *testing.T) {
	_, err := scope.NewPath("")
	assert.Error(t, err)
}

func TestNewPath_TildeExpansion(t *testing.T) {
	home := withHome(t)
	p, err := scope.NewPath("~/foo")
	require.NoError(t, err)
	// Home may be a symlink (macOS /var -> /private/var); both are acceptable.
	resolved, _ := filepath.EvalSymlinks(home)
	assert.Contains(t, []scope.Path{
		scope.Path(filepath.Join(home, "foo")),
		scope.Path(filepath.Join(resolved, "foo")),
	}, p)
}

func TestRules_DefensiveCopy(t *testing.T) {
	p := scope.New().Allow("/tmp/**")
	rules := p.Rules()
	rules[0].Patterns[0] = "mutated"
	rules2 := p.Rules()
	assert.Equal(t, scope.Pattern("/tmp/**"), rules2[0].Patterns[0])
}

func TestDefault_Singleton(t *testing.T) {
	a := scope.Default()
	b := scope.Default()
	assert.Same(t, a, b)
}
