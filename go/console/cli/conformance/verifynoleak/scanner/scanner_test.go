package scanner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/rules"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
)

func loadRules(t *testing.T) *rules.Set {
	t.Helper()
	set, err := rules.LoadDefault()
	require.NoError(t, err)
	return set
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

// ── YAML scanning ─────────────────────────────────────────────────

func TestScanFile_YAML_WithScenarioFiresRules(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "scenario.yaml", "scenario_id: x\nfoo: bar\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.False(t, res.Skipped)
	require.Len(t, res.Findings, 1)
	assert.Equal(t, "R1", res.Findings[0].RuleID)
	assert.Equal(t, 1, res.Findings[0].Line)
}

func TestScanFile_YAML_CleanFile_NoFindings(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "config.yaml", "name: my-app\nversion: 1\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.False(t, res.Skipped)
	assert.Empty(t, res.Findings)
}

func TestScanFile_YAML_ParseErrorIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "broken.yaml", "key: value\nkey: dup-key\n  bad indent\n: : :\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	// Either no findings + parse error, or findings + no parse error
	// — both are acceptable. The contract is we don't crash and we
	// don't block on parse failures alone.
	if res.ParseError != nil {
		assert.Empty(t, res.Findings)
	}
}

// ── Markdown scanning ─────────────────────────────────────────────

func TestScanFile_Markdown_FencedYAMLFiresRules(t *testing.T) {
	src := `# Header

prose here

` + "```yaml" + `
scenario_id: leaked-from-md
` + "```" + `

more prose
`
	dir := t.TempDir()
	p := writeFile(t, dir, "README.md", src)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	assert.Equal(t, "R1", f.RuleID)
	// Fence opens on line 5, content starts line 6, scenario_id is line 6 (relative 1) + offset 5 = 6.
	assert.Equal(t, 6, f.Line)
	assert.Equal(t, 5, f.BlockStartLine)
}

func TestScanFile_Markdown_NoFences_NoFindings(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "plain.md", "Just prose, no fences.\n")
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.Empty(t, res.Findings)
}

func TestScanFile_Markdown_OneBlockBrokenOneClean(t *testing.T) {
	// Broken YAML in first block, clean leak in second — second
	// should still fire.
	src := "```yaml\n: : :bad\n```\n\n```yaml\nscenario_id: real\n```\n"
	dir := t.TempDir()
	p := writeFile(t, dir, "mixed.md", src)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	require.NotEmpty(t, res.Findings)
	assert.Equal(t, "R1", res.Findings[0].RuleID)
}

// ── Skip + classify ──────────────────────────────────────────────

func TestScanFile_SkipsUnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "code.go", `package main`)
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped)
	assert.Contains(t, res.SkipReason, "unsupported")
}

func TestScanFile_SkipsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "big.yaml", strings.Repeat("k: v\n", 100))
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t), MaxFileSize: 10})
	assert.True(t, res.Skipped)
	assert.Contains(t, res.SkipReason, "size")
}

func TestScanFile_SkipsBinaryContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "binary.yaml")
	require.NoError(t, os.WriteFile(p, []byte{0x00, 0x01, 0x02, 0x03}, 0o644))
	res := scanner.ScanFile(p, scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped)
	assert.Contains(t, res.SkipReason, "binary")
}

func TestScanFile_SkipsMissingFile(t *testing.T) {
	res := scanner.ScanFile("/tmp/does/not/exist.yaml", scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped)
}

// ── Multi-file Scan ──────────────────────────────────────────────

func TestScan_MultipleFiles_CountFindings(t *testing.T) {
	dir := t.TempDir()
	p1 := writeFile(t, dir, "leak1.yaml", "scenario_id: a\n")
	p2 := writeFile(t, dir, "leak2.yaml", "scenario_id: b\n")
	p3 := writeFile(t, dir, "clean.yaml", "name: clean\n")
	results, err := scanner.Scan([]string{p1, p2, p3}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Equal(t, 2, scanner.CountFindings(results))
}

func TestScan_NilRules_ReturnsError(t *testing.T) {
	_, err := scanner.Scan(nil, scanner.Options{Rules: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil rules")
}

// ── ReaderScanFile (for commit-msg / network bodies) ─────────────

func TestReaderScanFile_MarkdownBody(t *testing.T) {
	body := "fix: foo\n\nhere's the scenario:\n```yaml\nscenario_id: in-commit\n```\n"
	res := scanner.ReaderScanFile("commit:abc1234", "md", strings.NewReader(body), scanner.Options{Rules: loadRules(t)})
	require.NotEmpty(t, res.Findings)
	assert.Equal(t, "commit:abc1234", res.Findings[0].Path)
}

func TestReaderScanFile_YAMLBody(t *testing.T) {
	res := scanner.ReaderScanFile("inline", "yaml", strings.NewReader("scenario_id: leaked\n"), scanner.Options{Rules: loadRules(t)})
	require.Len(t, res.Findings, 1)
}

func TestReaderScanFile_UnknownKind_Skips(t *testing.T) {
	res := scanner.ReaderScanFile("inline", "json", strings.NewReader(`{"key":"value"}`), scanner.Options{Rules: loadRules(t)})
	assert.True(t, res.Skipped)
}
