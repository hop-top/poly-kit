// Tests for the starter release-please config + manifest
// (releaseplease_seed.go): per-layout rendering, the org conventions
// hop-top/.github's release-please-preflight enforces, manifest seeding
// per release type, never-overwrite and idempotency.
package kitinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rpConfig   = ".github/release-please-config.json"
	rpManifest = ".github/.release-please-manifest.json"
)

type seedConfig struct {
	Schema                 string                    `json:"$schema"`
	SeparatePullRequests   bool                      `json:"separate-pull-requests"`
	PullRequestTitle       string                    `json:"pull-request-title-pattern"`
	ComponentNoSpace       bool                      `json:"component-no-space"`
	IncludeComponentInTag  bool                      `json:"include-component-in-tag"`
	TagSeparator           string                    `json:"tag-separator"`
	Label                  string                    `json:"label"`
	ReleaseLabel           string                    `json:"release-label"`
	ChangelogSections      []map[string]any          `json:"changelog-sections"`
	Packages               map[string]map[string]any `json:"packages"`
	UnexpectedLinkedVersns any                       `json:"linked-versions"`
}

func decodeSeed(t *testing.T, cfg, man []byte) (seedConfig, map[string]string) {
	t.Helper()
	var c seedConfig
	require.NoError(t, json.Unmarshal(cfg, &c), string(cfg))
	var m map[string]string
	require.NoError(t, json.Unmarshal(man, &m), string(man))
	return c, m
}

func pkg(releaseType, component string, exclude ...string) map[string]any {
	p := map[string]any{
		"release-type":         releaseType,
		"component":            component,
		"changelog-path":       "CHANGELOG.md",
		"bump-minor-pre-major": true,
		"prerelease":           true,
		"prerelease-type":      "alpha.0",
		"versioning":           "prerelease",
		"initial-version":      "0.1.0-alpha.0",
	}
	if len(exclude) > 0 {
		ex := make([]any, len(exclude))
		for i, e := range exclude {
			ex[i] = e
		}
		p["exclude-paths"] = ex
	}
	return p
}

// --- conventions -------------------------------------------------------------

func TestSeedConfig_OrgConventions(t *testing.T) {
	cfg, man := renderReleasePleaseSeed("demo", []string{"go"})
	c, m := decodeSeed(t, cfg, man)

	assert.Equal(t, "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json", c.Schema)
	assert.True(t, c.SeparatePullRequests)
	assert.Equal(t, "chore(release): ${component} ${version}", c.PullRequestTitle)
	assert.True(t, c.ComponentNoSpace)
	assert.True(t, c.IncludeComponentInTag)
	assert.Equal(t, "/", c.TagSeparator)
	assert.Equal(t, "status:release-pending", c.Label)
	assert.Equal(t, "status:release-tagged", c.ReleaseLabel)
	assert.Nil(t, c.UnexpectedLinkedVersns)
	assert.Equal(t, []map[string]any{
		{"type": "feat", "section": "Features"},
		{"type": "fix", "section": "Bug Fixes"},
		{"type": "perf", "section": "Performance"},
		{"type": "refactor", "section": "Refactored", "hidden": true},
		{"type": "chore", "section": "Miscellaneous", "hidden": true},
		{"type": "docs", "section": "Documentation", "hidden": true},
		{"type": "test", "section": "Tests", "hidden": true},
		{"type": "ci", "section": "CI", "hidden": true},
		{"type": "build", "section": "Build", "hidden": true},
	}, c.ChangelogSections)
	assert.Equal(t, map[string]map[string]any{".": pkg("go", "demo")}, c.Packages)
	assert.Equal(t, map[string]string{}, m)
	assert.True(t, strings.HasSuffix(string(cfg), "}\n"))
	assert.Equal(t, "{}\n", string(man))
}

// --- single-language layouts ---------------------------------------------------

// release-please 17.x: go / node / php use BaseStrategy.initialReleaseVersion,
// which honors `initial-version` when the manifest has no key, so `{}`
// makes the first release 0.1.0-alpha.0. python and rust override it
// with a hard-coded stable 0.1.0 and ignore `initial-version`, so they
// need a manifest seed (first release: 0.1.0-alpha.1).
func TestSeedConfig_SingleLanguage(t *testing.T) {
	cases := []struct {
		runtime, releaseType string
		manifest             map[string]string
	}{
		{"go", "go", map[string]string{}},
		{"ts", "node", map[string]string{}},
		{"php", "php", map[string]string{}},
		{"py", "python", map[string]string{".": "0.1.0-alpha.0"}},
		{"rs", "rust", map[string]string{".": "0.1.0-alpha.0"}},
	}
	for _, c := range cases {
		cfg, man := renderReleasePleaseSeed("demo", []string{c.runtime})
		got, m := decodeSeed(t, cfg, man)
		assert.Equal(t, map[string]map[string]any{".": pkg(c.releaseType, "demo")}, got.Packages, c.runtime)
		assert.Equal(t, c.manifest, m, c.runtime)
	}
}

// --- polyglot layouts --------------------------------------------------------------

func TestSeedConfig_Polyglot_GoRootPlusPorts(t *testing.T) {
	cfg, man := renderReleasePleaseSeed("demo", []string{"go", "ts", "py"})
	c, m := decodeSeed(t, cfg, man)
	assert.Equal(t, map[string]map[string]any{
		".":  pkg("go", "demo", "py", "ts"),
		"ts": pkg("node", "demo-ts"),
		"py": pkg("python", "demo-py"),
	}, c.Packages)
	// A python package needs a seed; seed every package so first
	// versions stay consistent across the repo.
	assert.Equal(t, map[string]string{".": "0.1.0-alpha.0", "ts": "0.1.0-alpha.0", "py": "0.1.0-alpha.0"}, m)
}

func TestSeedConfig_Polyglot_NoGo(t *testing.T) {
	cfg, man := renderReleasePleaseSeed("demo", []string{"ts", "php"})
	c, m := decodeSeed(t, cfg, man)
	assert.Equal(t, map[string]map[string]any{
		"ts":  pkg("node", "demo-ts"),
		"php": pkg("php", "demo-php"),
	}, c.Packages)
	assert.Equal(t, map[string]string{}, m)
}

func TestSeedConfig_RuntimeOrderAndDuplicatesDoNotMatter(t *testing.T) {
	a, am := renderReleasePleaseSeed("demo", []string{"py", "go", "ts"})
	b, bm := renderReleasePleaseSeed("demo", []string{"ts", "go", "py", "go", "nope"})
	assert.Equal(t, string(a), string(b))
	assert.Equal(t, string(am), string(bm))
}

func TestSeedConfig_NoKnownRuntime_DefaultsToGo(t *testing.T) {
	cfg, _ := renderReleasePleaseSeed("demo", nil)
	c, _ := decodeSeed(t, cfg, []byte("{}"))
	assert.Equal(t, map[string]map[string]any{".": pkg("go", "demo")}, c.Packages)
}

// The checks hop-top/.github's release-please-preflight.yml runs on the
// config: single-segment components, the prerelease four-piece combo,
// a counter digit in prerelease-type, prerelease-shaped seeds, no
// pyproject.toml extra-files on python packages.
func TestSeedConfig_SatisfiesPreflightInvariants(t *testing.T) {
	for _, rts := range [][]string{{"go"}, {"ts"}, {"py"}, {"rs"}, {"php"}, {"go", "ts", "py", "rs", "php"}} {
		cfg, man := renderReleasePleaseSeed("my-tool", rts)
		c, m := decodeSeed(t, cfg, man)
		for path, p := range c.Packages {
			comp, _ := p["component"].(string)
			assert.NotEmpty(t, comp, path)
			assert.NotContains(t, comp, "/", path)
			assert.Equal(t, true, p["prerelease"], path)
			assert.Equal(t, "prerelease", p["versioning"], path)
			assert.Equal(t, true, p["bump-minor-pre-major"], path)
			assert.Contains(t, p["prerelease-type"], ".", path)
			assert.NotContains(t, p, "extra-files", path)
			if seed, ok := m[path]; ok {
				assert.Contains(t, seed, "-", "%s seed %q must be prerelease-shaped", path, seed)
			}
		}
		for path := range m {
			assert.Contains(t, c.Packages, path, "manifest key %s has no package", path)
		}
	}
}

// --- writing through the generator ------------------------------------------------

func TestRenderReleasePlease_Fresh_SeedsConfigAndManifest(t *testing.T) {
	target := t.TempDir()
	in := rpInputs()
	in.Name = "demo"
	in.Runtime = []string{"go", "py"}
	actions, err := renderReleasePlease(target, in, fixedNow())
	require.NoError(t, err)

	wantCfg, wantMan := renderReleasePleaseSeed("demo", []string{"go", "py"})
	assert.Equal(t, string(wantCfg), readRel(t, target, rpConfig))
	assert.Equal(t, string(wantMan), readRel(t, target, rpManifest))

	a := mustAction(t, actions, rpConfig)
	assert.Equal(t, "write", a.Action)
	assert.Equal(t, "seed", a.Reason)
	assert.Contains(t, a.Detail, "status:release-pending")
	assert.Contains(t, a.Detail, "status:release-tagged")
	b := mustAction(t, actions, rpManifest)
	assert.Equal(t, "write", b.Action)
	assert.Equal(t, "seed", b.Reason)

	// Seeds are the repo's from birth: release-please rewrites the
	// manifest on every release, so kit does not track them.
	for _, f := range readManifestFile(t, target).Files {
		assert.NotEqual(t, rpConfig, f.Path)
		assert.NotEqual(t, rpManifest, f.Path)
	}
}

func TestRenderReleasePlease_NameFallsBackToDirectory(t *testing.T) {
	target := t.TempDir()
	_, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	var c seedConfig
	require.NoError(t, json.Unmarshal([]byte(readRel(t, target, rpConfig)), &c))
	assert.Equal(t, sanitizeComponent(filepath.Base(target)), c.Packages["."]["component"])
}

func TestRenderReleasePlease_Rerun_LeavesSeedsAlone(t *testing.T) {
	target := t.TempDir()
	in := rpInputs()
	in.Name = "demo"
	_, err := renderReleasePlease(target, in, fixedNow())
	require.NoError(t, err)
	// release-please then cuts a release and the owner edits the config.
	writeRel(t, target, rpManifest, `{".": "0.1.0-alpha.0"}`+"\n")
	edited := strings.Replace(readRel(t, target, rpConfig), `"changelog-path"`, `"draft": false, "changelog-path"`, 1)
	writeRel(t, target, rpConfig, edited)

	actions, err := renderReleasePlease(target, in, fixedNow())
	require.NoError(t, err)
	_, ok := findAction(actions, rpConfig)
	assert.False(t, ok, "%+v", actions)
	_, ok = findAction(actions, rpManifest)
	assert.False(t, ok, "%+v", actions)
	assert.Equal(t, edited, readRel(t, target, rpConfig))
	assert.Equal(t, `{".": "0.1.0-alpha.0"}`+"\n", readRel(t, target, rpManifest))
	assertAbsent(t, target, rpConfig+suggestedSuffix)
	assertAbsent(t, target, rpManifest+suggestedSuffix)
}

func TestRenderReleasePlease_ExistingConfigOnly_NeverOverwritten(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, rpConfig, `{"packages":{".":{}}}`)
	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, `{"packages":{".":{}}}`, readRel(t, target, rpConfig))
	assertAbsent(t, target, rpManifest)
	assert.Equal(t, "missing", mustAction(t, actions, rpManifest).Action)
}

func TestRenderReleasePlease_ExistingManifestOnly_NeverOverwritten(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, rpManifest, `{".": "1.2.3"}`)
	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, `{".": "1.2.3"}`, readRel(t, target, rpManifest))
	assertAbsent(t, target, rpConfig)
	assert.Equal(t, "missing", mustAction(t, actions, rpConfig).Action)
}

func TestRenderReleasePlease_RootLayout_NoSeedUnderGithub(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, "release-please-config.json", `{"packages":{".":{}}}`)
	_, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assertAbsent(t, target, rpConfig)
	assertAbsent(t, target, rpManifest)
}

func TestRenderReleasePlease_DryRun_ProjectsSeedsWritesNothing(t *testing.T) {
	target := t.TempDir()
	in := rpInputs()
	in.DryRun = true
	actions, err := renderReleasePlease(target, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "write", mustAction(t, actions, rpConfig).Action)
	assert.Equal(t, "write", mustAction(t, actions, rpManifest).Action)
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSanitizeComponent(t *testing.T) {
	for in, want := range map[string]string{
		"demo":       "demo",
		"My Tool":    "my-tool",
		"org/repo":   "org-repo",
		"-x.":        "x",
		"":           "app",
		"kit_v2.cli": "kit_v2.cli",
	} {
		assert.Equal(t, want, sanitizeComponent(in), in)
	}
	cfg, _ := renderReleasePleaseSeed("Org/My Tool", []string{"go", "ts"})
	c, _ := decodeSeed(t, cfg, []byte("{}"))
	assert.Equal(t, "org-my-tool", c.Packages["."]["component"])
	assert.Equal(t, "org-my-tool-ts", c.Packages["ts"]["component"])
}

// Seeds land only at the reusable workflow's default paths. A workflow
// pointing elsewhere keeps getting a missing report.
func TestRenderReleasePlease_CustomPathsMissing_ReportedNotSeeded(t *testing.T) {
	legacy := strings.Replace(legacyRootConfig, "config-file: release-please-config.json",
		"config-file: rp/config.json", 1)
	legacy = strings.Replace(legacy, "manifest-file: .release-please-manifest.json",
		"manifest-file: rp/manifest.json", 1)
	target := gitRepoWith(t, map[string]string{".github/workflows/release.yml": legacy})
	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "missing", mustAction(t, actions, "rp/config.json").Action)
	assertAbsent(t, target, rpConfig)
	assertAbsent(t, target, rpManifest)
	assertAbsent(t, target, "rp/config.json")
}
