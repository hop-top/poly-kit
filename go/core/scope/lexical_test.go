package scope_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

func checkLexical(t *testing.T, p *scope.Policy, path string, op scope.Op) scope.Decision {
	t.Helper()
	dec, err := p.CheckLexical(scope.Path(path), op)
	require.NoError(t, err)
	return dec
}

// symlinkTree builds <tmp>/real/sub/f and <tmp>/link -> <tmp>/real and
// returns the (unresolved) temp root.
func symlinkTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real", "sub", "f"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")))
	return root
}

// A rule written through a symlink names only the link path; a rule written
// on the target names only the target. Check (which resolves) would say
// Allowed for both spellings.
func TestCheckLexical_DoesNotResolveSymlinks(t *testing.T) {
	root := symlinkTree(t)

	viaLink := scope.New().Allow(scope.Pattern(root + "/link/**"))
	assert.Equal(t, scope.Allowed, checkLexical(t, viaLink, root+"/link/sub/f", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, viaLink, root+"/real/sub/f", scope.Read),
		"pattern written via the link must not cover the target spelling")

	viaReal := scope.New().Allow(scope.Pattern(root + "/real/**"))
	assert.Equal(t, scope.Allowed, checkLexical(t, viaReal, root+"/real/sub/f", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, viaReal, root+"/link/sub/f", scope.Read),
		"path through the link must not be resolved to the target")

	denyReal := scope.New().Allow(scope.Pattern(root + "/**")).Deny(scope.Pattern(root + "/real/**"))
	assert.Equal(t, scope.Allowed, checkLexical(t, denyReal, root+"/link/sub/f", scope.Read),
		"a deny on the target does not apply to the link spelling lexically")
	assert.Equal(t, scope.Denied, checkLexical(t, denyReal, root+"/real/sub/f", scope.Read))
}

// The answer must depend only on the strings: an existing symlink, an
// existing directory and a path that does not exist at all get the same
// verdict when they are spelled alike relative to the rules.
func TestCheckLexical_NoFilesystemOracle(t *testing.T) {
	root := symlinkTree(t)
	p := scope.New().Allow(scope.Pattern(root + "/granted/**"))

	for _, path := range []string{
		root + "/link/sub/f",    // exists, through a symlink into an ungranted dir
		root + "/real/sub/f",    // exists
		root + "/missing/sub/f", // does not exist
	} {
		assert.Equal(t, scope.Unknown, checkLexical(t, p, path, scope.Read), path)
	}

	// Pattern and path under a root that does not exist anywhere.
	ghost := scope.New().Allow("/no-such-root-kit-scope/a/**").Deny("/no-such-root-kit-scope/a/b/**")
	assert.Equal(t, scope.Allowed, checkLexical(t, ghost, "/no-such-root-kit-scope/a/x", scope.Write))
	assert.Equal(t, scope.Denied, checkLexical(t, ghost, "/no-such-root-kit-scope/a/b/c", scope.Write))
	assert.Equal(t, scope.Unknown, checkLexical(t, ghost, "/no-such-root-kit-scope/z", scope.Write))
}

// macOS: /tmp -> /private/tmp. Lexically a /tmp/** rule covers /tmp/x
// only; this holds on every platform since nothing is resolved.
func TestCheckLexical_TmpAsWritten(t *testing.T) {
	p := scope.New().Allow("/tmp/**")
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "/tmp/x", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, p, "/private/tmp/x", scope.Read))
}

// "~" in patterns expands to $HOME verbatim; a symlinked home is NOT
// canonicalised (Check's expandHome would resolve it).
func TestCheckLexical_TildeUsesHomeAsWritten(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "home-real")
	linkHome := filepath.Join(root, "home-link")
	require.NoError(t, os.MkdirAll(filepath.Join(realHome, "code"), 0o755))
	require.NoError(t, os.Symlink(realHome, linkHome))
	t.Setenv("HOME", linkHome)
	t.Setenv("USERPROFILE", linkHome)

	p := scope.New().Allow("~/code/**")
	assert.Equal(t, scope.Allowed, checkLexical(t, p, linkHome+"/code/x", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, p, realHome+"/code/x", scope.Read),
		"home must not be resolved through its symlink")

	// "~" in the path expands the same way.
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "~/code/x", scope.Read))
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "~/code", scope.Read))
}

func TestCheckLexical_TrailingDoubleStarMatchesDirItself(t *testing.T) {
	p := scope.New().AllowOp(scope.Read, "/p/**")
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "/p", scope.Read))
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "/p/", scope.Read))
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "/p/a/b", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, p, "/p-sibling", scope.Read))

	single := scope.New().AllowOp(scope.Read, "/p/*")
	assert.Equal(t, scope.Unknown, checkLexical(t, single, "/p", scope.Read))

	bare := scope.New().AllowOp(scope.Read, "/p")
	assert.Equal(t, scope.Allowed, checkLexical(t, bare, "/p", scope.Read))
	assert.Equal(t, scope.Unknown, checkLexical(t, bare, "/p/a", scope.Read))
}

func TestCheckLexical_CleansPath(t *testing.T) {
	p := scope.New().Allow("/a/b/**").Deny("/a/secret/**")
	assert.Equal(t, scope.Allowed, checkLexical(t, p, "/a/./b//c", scope.Read))
	assert.Equal(t, scope.Denied, checkLexical(t, p, "/a/b/../secret/k", scope.Read))
}

func TestCheckLexical_PerBitOps(t *testing.T) {
	tests := []struct {
		name string
		p    *scope.Policy
		op   scope.Op
		want scope.Decision
	}{
		{"read-only allow, read", scope.New().AllowOp(scope.Read, "/p/**"), scope.Read, scope.Allowed},
		{"read-only allow, write", scope.New().AllowOp(scope.Read, "/p/**"), scope.Write, scope.Unknown},
		{"read-only allow, read|write needs both", scope.New().AllowOp(scope.Read, "/p/**"), scope.Read | scope.Write, scope.Unknown},
		{"split allows cover read|write", scope.New().AllowOp(scope.Read, "/p/**").AllowOp(scope.Write, "/p/**"), scope.Read | scope.Write, scope.Allowed},
		{"allow all, deny write: read", scope.New().Allow("/p/**").DenyOp(scope.Write, "/p/**"), scope.Read, scope.Allowed},
		{"allow all, deny write: read|write", scope.New().Allow("/p/**").DenyOp(scope.Write, "/p/**"), scope.Read | scope.Write, scope.Denied},
		{"deny exec only, read|write|exec with no allow", scope.New().DenyOp(scope.Exec, "/p/**"), scope.Read | scope.Write | scope.Exec, scope.Denied},
		{"deny wins over allow on same bit", scope.New().AllowOp(scope.Write, "/p/**").DenyOp(scope.Write, "/p/x"), scope.Write, scope.Denied},
		{"deny on other bit ignored", scope.New().AllowOp(scope.Read, "/p/**").DenyOp(scope.Write, "/p/**"), scope.Read, scope.Allowed},
		{"no op requested", scope.New().Allow("/p/**"), 0, scope.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, checkLexical(t, tt.p, "/p/x", tt.op))
		})
	}
}

func TestCheckLexical_PathErrors(t *testing.T) {
	p := scope.New().Allow("**")
	for _, path := range []string{"", "relative/x", "./x", "~user/x"} {
		_, err := p.CheckLexical(scope.Path(path), scope.Read)
		assert.Error(t, err, "%q", path)
	}
}

func TestCheckLexical_BadPatternErrors(t *testing.T) {
	p := scope.New().Allow("/p/[")
	_, err := p.CheckLexical("/p/x", scope.Read)
	assert.Error(t, err)
}

func ExamplePolicy_CheckLexical() {
	p := scope.New().Allow("/tmp/**")

	named, _ := p.CheckLexical("/tmp/build/out", scope.Read)
	other, _ := p.CheckLexical("/private/tmp/build/out", scope.Read)
	fmt.Println(named == scope.Allowed, other == scope.Unknown)
	// Output: true true
}
