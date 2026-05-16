package verifynoleak_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/suppress"
)

// ── T-1227 suppression cases ─────────────────────────────────────

func loadAllowlist(t *testing.T, root, body string) *suppress.Allowlist {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".verifynoleak.allow"), []byte(body), 0o644))
	al, err := suppress.LoadAllowlist(root)
	require.NoError(t, err)
	return al
}

func TestSuppression_AllowlistHitSkipsFile(t *testing.T) {
	dir := t.TempDir()
	al := loadAllowlist(t, dir, "docs/**/*.md\n")
	p := writeFile(t, dir, "docs/leak.md", "```yaml\nscenario_id: should-be-allowlisted\nassertions:\n  - kind: exit_code_equals\n  - kind: stderr_contains\n```\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t), Allowlist: al})
	assert.True(t, res.Skipped)
	assert.Equal(t, "allowlisted", res.SkipReason)
	assert.Empty(t, res.Findings)
}

func TestSuppression_AllowlistNegationStillFires(t *testing.T) {
	// docs/** allowlisted, but docs/private/** is negated back in.
	dir := t.TempDir()
	al := loadAllowlist(t, dir, "docs/**\n!docs/private/**\n")
	p := writeFile(t, dir, "docs/private/leak.md", "```yaml\nscenario_id: still-detected\n```\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t), Allowlist: al})
	assert.False(t, res.Skipped, "negated path must be scanned: %+v", res)
	assert.NotEmpty(t, res.Findings)
}

func TestSuppression_YAMLFileLevelIgnoreWithReason(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "scenario.yaml", "# verify-no-leak: ignore — kit threat-model sample\nscenario_id: illustrative\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped, "file-level ignore directive must skip the whole file")
	assert.Contains(t, res.SkipReason, "ignore comment")
	assert.Empty(t, res.Findings)
}

func TestSuppression_MarkdownFileLevelIgnoreWithReason(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.md", `<!-- verify-no-leak: ignore — schema docs sample -->

# Title

`+"```yaml"+`
scenario_id: example
`+"```"+`

Another block:

`+"```yaml"+`
scenario_id: another-example
`+"```"+`
`)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped, "file-level MD ignore must skip every block: %+v", res)
	assert.Empty(t, res.Findings)
}

func TestSuppression_BareIgnoreYAML_RejectedAsConfigError(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "scenario.yaml", "# verify-no-leak: ignore\nscenario_id: x\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.NotNil(t, res.ParseError, "bare-ignore must surface as a ParseError")
	assert.True(t, errors.Is(res.ParseError, suppress.ErrBareIgnoreRejected))
	// The scanner skips the file when it detects bare-ignore so we
	// don't accidentally also report findings for a file the author
	// intended to suppress.
	assert.Empty(t, res.Findings)
}

func TestSuppression_BareIgnoreMarkdown_RejectedAsConfigError(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.md", "<!-- verify-no-leak: ignore -->\n\n```yaml\nscenario_id: x\n```\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.NotNil(t, res.ParseError)
	assert.True(t, errors.Is(res.ParseError, suppress.ErrBareIgnoreRejected))
}

func TestSuppression_EmptyReasonAfterDash_StillRejected(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "scenario.yaml", "# verify-no-leak: ignore —    \nscenario_id: x\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.NotNil(t, res.ParseError, "whitespace-only reason is the same as no reason")
	assert.True(t, errors.Is(res.ParseError, suppress.ErrBareIgnoreRejected))
}

func TestSuppression_IgnoreNextBlockScopesToNextFenceOnly(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.md", `# Title

<!-- verify-no-leak: ignore-next-block — schema illustration -->
`+"```yaml"+`
scenario_id: ignored-first-block
`+"```"+`

`+"```yaml"+`
scenario_id: second-block-still-fires
`+"```"+`
`)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.False(t, res.Skipped)
	require.Len(t, res.Findings, 1, "second block must fire, first must be suppressed")
	assert.Contains(t, res.Findings[0].Description, "scenario_id")
}

func TestSuppression_IgnoreNextBlock_OutOfScopeDistance(t *testing.T) {
	// Directive on line 1, fence on line 6 (distance 5 > limit 3):
	// the directive should NOT cover the fence.
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.md", `<!-- verify-no-leak: ignore-next-block — too far -->


prose

`+"```yaml"+`
scenario_id: not-actually-covered
`+"```"+`
`)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.NotEmpty(t, res.Findings, "directive >3 lines before fence must not cover it")
}
