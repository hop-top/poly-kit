// Package kitinit augment_test.go — tier matrix + conflict semantics
// for runAugment. Synthetic fixture templates keep file sets predictable
// so each tier's contract (per spec §13) is asserted directly.
//
// White-box (package kitinit) so we can call unexported runAugment.
package kitinit

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmpl "hop.top/kit/internal/template"
)

// recordingHookRunner lives in testhelpers_test.go (T-0952). For augment,
// hooks are empty in the synthetic fixture so calls stays nil — kept as
// a forward-compat probe.

// fixtureTemplate writes a synthetic kit-template.yaml + tiers.yaml + a
// canonical file set to a temp dir and returns its absolute path. The
// file set covers all four tiers so a single fixture serves every test:
//
//	tier 1: lint.yml, Makefile, .gitignore
//	tier 2: + ci.yml (CI workflow)
//	tier 3: + cmd/main.go
//	tier 4: + README.md, docs/getting-started.md, toolspec.yaml
func fixtureTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	manifest := `name: fixture
description: "synthetic fixture for augment_test"
kit_version: ">=0.4.0"
variables:
  - name: name
    prompt: "name"
  - name: module
    prompt: "module"
files:
  exclude:
    - "kit-template.yaml"
    - "tiers.yaml"
render_rules:
  strip_suffixes: [".tmpl"]
hooks: {}
`
	// Tier-map keys are post-substitution OUTPUT paths. The engine
	// resolves "{{.name}}" in source paths but does NOT substitute
	// keys in tiers.yaml — so we keep tier-mapped paths var-free.
	tiers := `files:
  "lint.yml": [1, 2, 3, 4]
  "Makefile": [1, 2, 3, 4]
  ".gitignore": [1, 2, 3, 4]
  ".github/workflows/ci.yml": [2, 3, 4]
  "cmd/main.go": [3, 4]
  "README.md": [4]
  "docs/getting-started.md": [4]
  "toolspec.yaml": [4]
`
	files := map[string]string{
		"kit-template.yaml": manifest,
		"tiers.yaml":        tiers,

		"lint.yml":                     "rules: []\n",
		"Makefile":                     "build:\n\t@echo build\n",
		".gitignore":                   "/bin\n",
		".github/workflows/ci.yml":     "name: ci\n",
		"cmd/main.go.tmpl":             "package main // {{.name}}\n",
		"README.md.tmpl":               "# {{.name}}\n",
		"docs/getting-started.md.tmpl": "# Start {{.name}}\n",
		"toolspec.yaml.tmpl":           "name: {{.name}}\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o640))
	}
	return dir
}

// fixtureDeps returns Deps wired with a fresh registry + a recording hook
// runner. Git/GitHub stay nil — augment never touches them.
func fixtureDeps() (Deps, *recordingHookRunner) {
	hooks := &recordingHookRunner{}
	return Deps{
		Registry: tmpl.NewRegistry("", ""),
		Hooks:    hooks,
		Output:   nil,
	}, hooks
}

// baseInputs builds an Inputs struct primed for runAugment against the
// fixture template. Vars carries name+module so the .tmpl files render.
func baseInputs(templatePath, name string, tier int) Inputs {
	return Inputs{
		Name:     name,
		Module:   "github.com/me/" + name,
		Template: templatePath,
		Tier:     tier,
		Vars: map[string]any{
			"name":   name,
			"module": "github.com/me/" + name,
		},
	}
}

// initGitDir creates a bare .git/ marker so the fixture passes for any
// "must be in an existing repo" check augment may add later. runAugment
// itself does not require git presence today, but we mirror the spec
// fixture shape (existing repo).
func initGitDir(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o750))
}

// listRel returns the absolute paths in res relative to root, sorted.
// Used to assert exact tier file sets.
func listRel(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			out = append(out, p)
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func TestAugment_Tier1_FileSet(t *testing.T) {
	tplPath := fixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 1)

	sum, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)

	got := listRel(cwd, sum.Result.Written)
	want := []string{".gitignore", "Makefile", "lint.yml"}
	assert.Equal(t, want, got, "tier 1 must include only lint/build files")

	for _, absent := range []string{
		".github/workflows/ci.yml", "cmd/demo/main.go",
		"README.md", "docs/getting-started.md", "toolspec.yaml",
	} {
		_, statErr := os.Stat(filepath.Join(cwd, absent))
		assert.True(t, os.IsNotExist(statErr),
			"tier 1 must not write %s; got stat err = %v", absent, statErr)
	}
}

func TestAugment_Tier2_AddsCI(t *testing.T) {
	tplPath := fixtureTemplate(t)

	cwd1 := t.TempDir()
	initGitDir(t, cwd1)
	deps1, _ := fixtureDeps()
	sum1, err := runAugment(context.Background(), deps1, baseInputs(tplPath, "demo", 1), cwd1)
	require.NoError(t, err)
	t1 := listRel(cwd1, sum1.Result.Written)

	cwd2 := t.TempDir()
	initGitDir(t, cwd2)
	deps2, _ := fixtureDeps()
	sum2, err := runAugment(context.Background(), deps2, baseInputs(tplPath, "demo", 2), cwd2)
	require.NoError(t, err)
	t2 := listRel(cwd2, sum2.Result.Written)

	// diff(t2, t1) == CI workflows added by tier 2.
	diff := setDiff(t2, t1)
	assert.Equal(t, []string{".github/workflows/ci.yml"}, diff,
		"tier 2 should add CI workflow on top of tier 1")
}

func TestAugment_Tier3_AddsMain(t *testing.T) {
	tplPath := fixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	// Pre-create cmd/main.go with custom content; engine must route
	// the rendered template to .kit-suggested sibling and leave the
	// original untouched.
	//
	// NOTE: The fixture's tier map uses cmd/main.go (no name var) —
	// engine does not substitute tier-map keys. Real cli-go template
	// places main.go at the root for the same reason.
	customMain := []byte("package main // CUSTOM USER CODE\n")
	mainPath := filepath.Join(cwd, "cmd", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(mainPath), 0o750))
	require.NoError(t, os.WriteFile(mainPath, customMain, 0o640))

	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 3)
	sum, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)

	// Original main.go untouched.
	got, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	assert.Equal(t, string(customMain), string(got),
		"existing main.go must remain unchanged")

	// .kit-suggested sibling carries the rendered template content.
	suggested := mainPath + ".kit-suggested"
	gotSug, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, "package main // demo\n", string(gotSug))
	assert.Contains(t, sum.Result.Suggested, suggested)
}

func TestAugment_Tier4_FullConformance(t *testing.T) {
	tplPath := fixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	deps, _ := fixtureDeps()
	sum, err := runAugment(context.Background(), deps, baseInputs(tplPath, "demo", 4), cwd)
	require.NoError(t, err)

	got := listRel(cwd, sum.Result.Written)
	for _, want := range []string{
		"README.md",
		"docs/getting-started.md",
		"toolspec.yaml",
	} {
		assert.Contains(t, got, want, "tier 4 must include %s", want)
	}
}

// TestAugment_Force_OverwritesNonMain — T-0949: Inputs.Force plumbs into
// engine.Render. Non-sacred files (README.md) overwrite; sacred files
// (cmd/<name>/main.go) still route to .kit-suggested.
//
// Uses a bespoke fixture so the engine writes to cmd/{{.name}}/main.go
// (matching the sacred glob cmd/*/main.go) — the shared fixture uses a
// flat cmd/main.go which doesn't exercise the sacred-path policy.
func TestAugment_Force_OverwritesNonMain(t *testing.T) {
	tplPath := forceFixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	// Pre-existing non-sacred file.
	oldReadme := []byte("# old\n")
	readmePath := filepath.Join(cwd, "README.md")
	require.NoError(t, os.WriteFile(readmePath, oldReadme, 0o640))

	// Pre-existing sacred file at cmd/demo/main.go.
	customMain := []byte("package main // CUSTOM USER CODE\n")
	mainDir := filepath.Join(cwd, "cmd", "demo")
	require.NoError(t, os.MkdirAll(mainDir, 0o750))
	mainPath := filepath.Join(mainDir, "main.go")
	require.NoError(t, os.WriteFile(mainPath, customMain, 0o640))

	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 4)
	in.Force = true

	sum, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)

	// Non-sacred README.md overwritten with template content.
	gotReadme, err := os.ReadFile(readmePath)
	require.NoError(t, err)
	assert.Equal(t, "# demo\n", string(gotReadme),
		"force=true must overwrite non-sacred README.md")
	_, statErr := os.Stat(readmePath + ".kit-suggested")
	assert.True(t, os.IsNotExist(statErr),
		"no .kit-suggested sibling for force-overwritten non-sacred file")
	assert.NotContains(t, sum.Result.Suggested, readmePath+".kit-suggested")

	// Sacred cmd/demo/main.go untouched even with force=true.
	gotMain, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	assert.Equal(t, string(customMain), string(gotMain),
		"force=true must NOT overwrite sacred cmd/*/main.go")
	suggested := mainPath + ".kit-suggested"
	gotSug, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, "package main // demo\n", string(gotSug),
		"sacred conflict must emit .kit-suggested sibling with rendered content")
	assert.Contains(t, sum.Result.Suggested, suggested)
}

// forceFixtureTemplate writes a tiny template whose tier-4 file set
// resolves to README.md + cmd/{{.name}}/main.go after path-segment
// substitution. Used only by TestAugment_Force_OverwritesNonMain.
func forceFixtureTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `name: force-fixture
description: "force-overwrite fixture"
kit_version: ">=0.4.0"
variables:
  - name: name
    prompt: "name"
files:
  exclude:
    - "kit-template.yaml"
    - "tiers.yaml"
render_rules:
  strip_suffixes: [".tmpl"]
hooks: {}
`
	// Tier-map keys are post-substitution paths but the engine does NOT
	// substitute keys → use the literal "demo" name baked into the test.
	tiers := `files:
  "README.md": [4]
  "cmd/demo/main.go": [4]
`
	files := map[string]string{
		"kit-template.yaml":          manifest,
		"tiers.yaml":                 tiers,
		"README.md.tmpl":             "# {{.name}}\n",
		"cmd/{{.name}}/main.go.tmpl": "package main // {{.name}}\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o640))
	}
	return dir
}

func TestAugment_Idempotent(t *testing.T) {
	tplPath := fixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 4)

	// First run: writes everything fresh.
	sum1, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)
	require.NotEmpty(t, sum1.Result.Written, "first run must produce writes")

	// Second run with identical inputs: every file is byte-identical
	// → engine "silent skip" path → zero Written, zero Suggested.
	sum2, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)
	assert.Empty(t, sum2.Result.Written,
		"idempotent run: no files should be re-written; got %v", sum2.Result.Written)
	assert.Empty(t, sum2.Result.Suggested,
		"idempotent run: no .kit-suggested siblings; got %v", sum2.Result.Suggested)
}

func TestAugment_PreservesGoModule(t *testing.T) {
	tplPath := fixtureTemplate(t)
	cwd := t.TempDir()
	initGitDir(t, cwd)

	// Pre-create go.mod with a custom module path. Augment must not
	// overwrite it; the readGoModule fallback should populate
	// Inputs.Module from the existing file when in.Module starts empty.
	customMod := "module github.com/me/mything\n\ngo 1.22\n"
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "go.mod"), []byte(customMod), 0o640))

	deps, _ := fixtureDeps()
	// in.Module="" so runAugment falls back to readGoModule(cwd).
	in := Inputs{
		Name:     "demo",
		Template: tplPath,
		Tier:     4,
		Vars: map[string]any{
			"name": "demo",
			// module deliberately omitted → readGoModule populates it
		},
	}

	_, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)

	// go.mod content unchanged.
	got, err := os.ReadFile(filepath.Join(cwd, "go.mod"))
	require.NoError(t, err)
	assert.Equal(t, customMod, string(got),
		"go.mod must not be overwritten by augment")
}

// setDiff returns elements in a not present in b (sorted).
func setDiff(a, b []string) []string {
	have := make(map[string]struct{}, len(b))
	for _, x := range b {
		have[x] = struct{}{}
	}
	var out []string
	for _, x := range a {
		if _, ok := have[x]; !ok {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
