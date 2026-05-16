package story_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/scenariorules"
	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/validator"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/rules"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
)

// TestExampleStoriesPassBothValidators is the structural
// leak-resistance guarantee in test form: every reference story
// under examples/spaced/e2e/stories/ must pass both
//
//   - verify-stories (the story validator), and
//   - verify-no-leak (the leak detector),
//
// on the same input bytes. The validator + the detector consume
// the same canonical contracts/scenario-rules.json via the shared
// scenariorules loader, so this is the live cross-check that the
// closed-key story schema produces output the leak detector never
// flags. If a future change to either side trips this, the regression
// is the design contract breaking, not a flaky test.
func TestExampleStoriesPassBothValidators(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	pkgDir := filepath.Dir(thisFile)
	// go/conformance/story → up four to repo root.
	storiesDir := filepath.Join(pkgDir, "..", "..", "..", "examples", "spaced", "e2e", "stories")

	matches, err := filepath.Glob(filepath.Join(storiesDir, "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "no reference stories under %s", storiesDir)

	doc, err := scenariorules.LoadDefault()
	require.NoError(t, err)
	set, err := rules.LoadDefault()
	require.NoError(t, err)

	for _, p := range matches {
		t.Run(filepath.Base(p), func(t *testing.T) {
			// verify-stories side
			ps, err := parser.ParseFile(p)
			require.NoError(t, err, "story must parse")
			fs := validator.ValidateOne(ps, validator.Options{Rules: doc, RepoRoot: pkgDir})
			for _, f := range fs {
				if f.Severity == validator.SeverityError {
					t.Errorf("verify-stories error: %s — %s", f.Rule, f.Message)
				}
			}

			// verify-no-leak side
			results, err := scanner.Scan([]string{p}, scanner.Options{Rules: set})
			require.NoError(t, err)
			for _, r := range results {
				for _, f := range r.Findings {
					t.Errorf("verify-no-leak finding on a valid story: %s — %s (matched %v)", f.RuleID, f.Description, f.MatchedKeys)
				}
			}
		})
	}
}

// TestScenarioShapedFileFailsBothValidators is the negative side: a
// "story" that smuggles in scenario_id at root should fail BOTH
// validators. This guards the design.md §5 belt-and-suspenders
// claim that CI's two invocations catch every malicious story.
func TestScenarioShapedFileFailsBothValidators(t *testing.T) {
	bad := []byte(`schema_version: "1"
story_id: a.b.c
title: t
binary: spaced
intent: A reasonably long intent that satisfies the forty character minimum for shape.
scenario_id: oops.scenario.x
assertions:
  - kind: exit_code_equals
    value: 0
  - kind: stderr_contains
    value: ok
steps:
  - id: x
    invoke: ["spaced", "mission", "list"]
`)

	// verify-stories side
	_, perr := parser.ParseBytes(bad, "bad.yaml")
	require.Error(t, perr, "scenario-shaped YAML must fail the closed-key parse")

	// verify-no-leak side: write to a tempfile so scanner can pick
	// up its file extension.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, bad, 0644))
	set, err := rules.LoadDefault()
	require.NoError(t, err)
	results, err := scanner.Scan([]string{path}, scanner.Options{Rules: set})
	require.NoError(t, err)
	var any int
	for _, r := range results {
		any += len(r.Findings)
	}
	require.NotZero(t, any, "scenario-shaped YAML must produce leak findings")
}
