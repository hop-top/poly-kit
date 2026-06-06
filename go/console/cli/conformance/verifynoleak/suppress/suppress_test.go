package suppress_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/suppress"
)

// ── Allowlist ─────────────────────────────────────────────────────

func TestLoadAllowlist_MissingFileReturnsEmpty(t *testing.T) {
	al, err := suppress.LoadAllowlist(t.TempDir())
	require.NoError(t, err)
	assert.True(t, al.Empty())
	assert.False(t, al.Matches("anything"))
}

func TestLoadAllowlist_ParsesPatternsAndComments(t *testing.T) {
	dir := t.TempDir()
	body := `# allowlist for tests
docs/**/*.md
!docs/sensitive/**

contracts/example.json
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".verifynoleak.allow"), []byte(body), 0o644))
	al, err := suppress.LoadAllowlist(dir)
	require.NoError(t, err)
	assert.False(t, al.Empty())
}

func TestAllowlist_GlobMatchesAndNegations(t *testing.T) {
	dir := t.TempDir()
	body := `docs/**/*.md
!docs/sensitive/**
contracts/example.json
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".verifynoleak.allow"), []byte(body), 0o644))
	al, err := suppress.LoadAllowlist(dir)
	require.NoError(t, err)

	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(dir, "docs/intro.md"), true},
		{filepath.Join(dir, "docs/topic/page.md"), true},
		{filepath.Join(dir, "docs/sensitive/leak.md"), false}, // negated
		{filepath.Join(dir, "contracts/example.json"), true},
		{filepath.Join(dir, "src/foo.go"), false}, // never matched
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, al.Matches(c.path), "path %s", c.path)
	}
}

func TestAllowlist_AddAppendsPatterns(t *testing.T) {
	dir := t.TempDir()
	al, err := suppress.LoadAllowlist(dir)
	require.NoError(t, err)
	al.Add("foo/**", "!foo/private/**")

	assert.True(t, al.Matches(filepath.Join(dir, "foo/bar.yaml")))
	assert.False(t, al.Matches(filepath.Join(dir, "foo/private/bar.yaml")))
}

func TestAllowlist_DefaultKitInternalGlobsCovers12fccTracks(t *testing.T) {
	dir := t.TempDir()
	al, err := suppress.LoadAllowlist(dir)
	require.NoError(t, err)
	al.Add(suppress.DefaultKitInternalGlobs()...)
	// Each canonical 12fcc track doc should be allowlisted.
	for _, sub := range []string{
		".tlc/tracks/12fcc-leak/design.md",
		".tlc/tracks/12fcc/spec.md",
		".tlc/tracks/12fcc-scen/anything.md",
		"contracts/scenario-rules.json",
		"go/conformance/scenario/README.md",
		"go/conformance/scenario/testdata/ok-minimal.yaml",
	} {
		assert.True(t, al.Matches(filepath.Join(dir, sub)), "kit-internal glob should cover %s", sub)
	}
}

// ── Ignore directives ────────────────────────────────────────────

func TestParseIgnoreDirectives_YAMLFile_AcceptsEmDash(t *testing.T) {
	src := []byte("# verify-no-leak: ignore — kit threat-model sample\nscenario_id: x\n")
	ds, err := suppress.ParseIgnoreDirectives(src, "yaml")
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, suppress.IgnoreFile, ds[0].Kind)
	assert.Equal(t, "kit threat-model sample", ds[0].Reason)
}

func TestParseIgnoreDirectives_YAMLFile_AcceptsDoubleDash(t *testing.T) {
	src := []byte("# verify-no-leak: ignore -- for environments without em-dash\nscenario_id: x\n")
	ds, err := suppress.ParseIgnoreDirectives(src, "yaml")
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Contains(t, ds[0].Reason, "em-dash")
}

func TestParseIgnoreDirectives_RejectsBareIgnore(t *testing.T) {
	src := []byte("# verify-no-leak: ignore\nscenario_id: x\n")
	_, err := suppress.ParseIgnoreDirectives(src, "yaml")
	require.Error(t, err)
	assert.ErrorIs(t, err, suppress.ErrBareIgnoreRejected)
}

func TestParseIgnoreDirectives_RejectsEmptyReason(t *testing.T) {
	// "ignore —" with only whitespace after the dash is still bare.
	src := []byte("# verify-no-leak: ignore —    \nscenario_id: x\n")
	_, err := suppress.ParseIgnoreDirectives(src, "yaml")
	require.Error(t, err)
	assert.ErrorIs(t, err, suppress.ErrBareIgnoreRejected)
}

func TestParseIgnoreDirectives_YAMLLookahead5Lines(t *testing.T) {
	// Directive on line 6 must not be picked up — lookahead is 5.
	src := []byte("# line 1\n# line 2\n# line 3\n# line 4\n# line 5\n# verify-no-leak: ignore — too late\nscenario_id: x\n")
	ds, err := suppress.ParseIgnoreDirectives(src, "yaml")
	require.NoError(t, err)
	assert.Empty(t, ds, "directive past lookahead must be ignored")
}

func TestParseIgnoreDirectives_MarkdownHTMLComment(t *testing.T) {
	src := []byte("<!-- verify-no-leak: ignore — kit threat-model sample -->\n\n# title\n")
	ds, err := suppress.ParseIgnoreDirectives(src, "md")
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, suppress.IgnoreFile, ds[0].Kind)
}

func TestParseIgnoreDirectives_MarkdownIgnoreNextBlock(t *testing.T) {
	src := []byte("intro prose\n\n<!-- verify-no-leak: ignore-next-block — schema example -->\n```yaml\nscenario_id: schema-doc\n```\n")
	ds, err := suppress.ParseIgnoreDirectives(src, "md")
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, suppress.IgnoreNextBlock, ds[0].Kind)
	assert.Equal(t, 3, ds[0].Line)
}

func TestFindIgnoreNextBlockFor_ScopeRespected(t *testing.T) {
	// Directive on line 3, fence on line 4 → covered (distance 1).
	// Directive on line 10, fence on line 20 → too far (distance 10).
	ds := []suppress.IgnoreDirective{
		{Kind: suppress.IgnoreNextBlock, Line: 3, Reason: "close"},
		{Kind: suppress.IgnoreNextBlock, Line: 10, Reason: "far"},
	}
	_, ok := suppress.FindIgnoreNextBlockFor(ds, 4)
	assert.True(t, ok, "fence directly after directive should be covered")

	_, ok = suppress.FindIgnoreNextBlockFor(ds, 20)
	assert.False(t, ok, "fence 10 lines past directive must not be covered (limit is 3)")
}

func TestFindIgnoreNextBlockFor_DirectiveAfterFence_Ignored(t *testing.T) {
	ds := []suppress.IgnoreDirective{
		{Kind: suppress.IgnoreNextBlock, Line: 10, Reason: "after the fact"},
	}
	_, ok := suppress.FindIgnoreNextBlockFor(ds, 5)
	assert.False(t, ok, "directive after fence cannot retroactively cover it")
}

func TestHasFileLevelIgnore(t *testing.T) {
	ds := []suppress.IgnoreDirective{
		{Kind: suppress.IgnoreNextBlock, Line: 2, Reason: "x"},
	}
	assert.False(t, suppress.HasFileLevelIgnore(ds))

	ds = append(ds, suppress.IgnoreDirective{Kind: suppress.IgnoreFile, Line: 1, Reason: "y"})
	assert.True(t, suppress.HasFileLevelIgnore(ds))
}

func TestErrBareIgnoreRejected_IsSentinel(t *testing.T) {
	require.True(t, errors.Is(suppress.ErrBareIgnoreRejected, suppress.ErrBareIgnoreRejected))
}
