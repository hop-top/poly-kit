// Package verifynoleak_test holds black-box integration tests that
// span rules + extractor + scanner + suppress. Unit tests for each
// component live in the per-package _test.go files. This file
// exercises the wired pipeline end-to-end on file trees built in
// t.TempDir(), confirming the scenarios called out in the survey's
// threat model.
package verifynoleak_test

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

func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	return full
}

func findingsByRule(fs []scanner.Finding) map[string][]scanner.Finding {
	out := map[string][]scanner.Finding{}
	for _, f := range fs {
		out[f.RuleID] = append(out[f.RuleID], f)
	}
	return out
}

func allFindings(results []scanner.FileResult) []scanner.Finding {
	var out []scanner.Finding
	for _, r := range results {
		out = append(out, r.Findings...)
	}
	return out
}

// ── T-1225 positive cases ────────────────────────────────────────

func TestPositive_RealScenarioAtTypicalPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "scenarios/launch.yaml", `scenario_id: spaced.launch.dry-run-clean
assertions:
  - kind: exit_code_equals
    value: 0
  - kind: cassette_must_not_contain
    pattern: SECRET
steps:
  - run: launch
    expect:
      cassette_must_not_contain: PASSWORD
judge:
  prompt: rate clarity
  required_score: 7
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)

	by := findingsByRule(allFindings(results))
	// All four rules fire on a maximally leak-shaped doc.
	assert.NotEmpty(t, by["R1"], "scenario_id at root must fire R1")
	assert.NotEmpty(t, by["R2"], ">=2 known verbs in assertions must fire R2")
	assert.NotEmpty(t, by["R3"], "cassette_must_not_contain as key must fire R3")
	assert.NotEmpty(t, by["R4"], "judge with prompt+score must fire R4")
}

func TestPositive_ReadmeFencedYAMLBlock(t *testing.T) {
	// Survey §1: the riskiest leak channel. An author illustrating
	// "what a scenario looks like" in a README.
	dir := t.TempDir()
	p := writeFile(t, dir, "README.md", `# Example app

Here is what a scenario looks like:

`+"```yaml"+`
scenario_id: example.illustrative
assertions:
  - kind: exit_code_equals
  - kind: stderr_contains
`+"```"+`

More prose follows.
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)

	fs := allFindings(results)
	require.NotEmpty(t, fs, "fenced YAML in README must trigger findings")

	for _, f := range fs {
		assert.Equal(t, p, f.Path)
		assert.Equal(t, 5, f.BlockStartLine, "fence opens on line 5")
		assert.GreaterOrEqual(t, f.Line, 6, "finding line is file-relative, not block-relative")
	}
}

func TestPositive_CommitMessageBodyFencedYAML(t *testing.T) {
	body := `fix: add launch command

Here's the scenario this needs to pass:

` + "```yaml" + `
scenario_id: spaced.launch
assertions:
  - kind: exit_code_equals
  - kind: cassette_must_not_contain
` + "```" + `
`
	res := scanner.ReaderScanFile("commit:abc1234", "md", strings.NewReader(body), scanner.Options{Rules: loadRules(t)})
	require.NotEmpty(t, res.Findings)
	assert.Equal(t, "commit:abc1234", res.Findings[0].Path)
}

func TestPositive_MultiBlockMarkdownTwoLeaksOneClean(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.md", `# Scenarios

Bad block one:

`+"```yaml"+`
scenario_id: leak-one
`+"```"+`

A clean config block:

`+"```yaml"+`
name: my-tool
version: 1
`+"```"+`

Bad block two:

`+"```yaml"+`
scenario_id: leak-two
`+"```"+`
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)

	fs := allFindings(results)
	// Two R1 findings (one per leak block), zero from the clean block.
	r1 := 0
	for _, f := range fs {
		if f.RuleID == "R1" {
			r1++
		}
	}
	assert.Equal(t, 2, r1, "expected exactly two R1 findings, got %d", r1)

	// Block-start lines must differ — confirms blocks are scanned
	// independently rather than as one merged document.
	starts := map[int]bool{}
	for _, f := range fs {
		starts[f.BlockStartLine] = true
	}
	assert.Len(t, starts, 2, "two findings must come from two distinct fences")
}

func TestPositive_NestedScenariosDirAndScenarioSuffix(t *testing.T) {
	dir := t.TempDir()
	p1 := writeFile(t, dir, "services/foo/scenarios/launch.scenario.yaml", `scenario_id: nested.foo
assertions:
  - kind: exit_code_equals
  - kind: stderr_contains
`)
	p2 := writeFile(t, dir, "services/bar/scenarios/run.yaml", `scenario_id: nested.bar
`)
	results, err := scanner.Scan([]string{p1, p2}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)

	// p1 trips R1 (scenario_id) + R2 (>=2 known verbs). p2 trips R1 only.
	// Total: 3 findings across 2 files. The point of this test is that
	// nested-path scanning happens at all; the exact rule count is a
	// secondary check.
	per := map[string]int{}
	for _, r := range results {
		per[r.Path] = len(r.Findings)
	}
	assert.Equal(t, 2, per[p1], "deep-nested scenario file should fire R1 and R2")
	assert.Equal(t, 1, per[p2], "scenario.yaml suffix file should fire R1")
}
