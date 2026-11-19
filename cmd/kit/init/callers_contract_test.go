// Caller-contract tests for the cli-go scaffold: every workflow kit init
// renders must be runnable against the hop-top/.github reusable workflows
// it calls.
//
// The reusable workflows' `on.workflow_call` blocks are vendored under
// testdata/dotgithub-v<version>/ (header through `on:`, at the commit
// workflowCallerRef pins), so the test needs no network. Bumping
// workflowCallerRef without refreshing the fixtures fails
// TestCallersContract_FixturesMatchPin.
package kitinit

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Absolute: the bootstrap helpers chdir into a temp dir.
var callersFixtureDir = func() string {
	d, err := filepath.Abs("testdata/dotgithub-" + workflowCallerVersion)
	if err != nil {
		panic(err)
	}
	return d
}()

var dotgithubUsesRe = regexp.MustCompile(`^hop-top/\.github/\.github/workflows/([A-Za-z0-9._-]+\.yml)@(\S+)`)

type wfDoc struct {
	On   map[string]any `yaml:"on"`
	Jobs map[string]struct {
		Uses    string            `yaml:"uses"`
		With    map[string]any    `yaml:"with"`
		Secrets any               `yaml:"secrets"`
		Steps   []map[string]any  `yaml:"steps"`
		Needs   any               `yaml:"needs"`
		Name    string            `yaml:"name"`
		Env     map[string]string `yaml:"env"`
	} `yaml:"jobs"`
}

func loadWF(t *testing.T, path string) wfDoc {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, path)
	var d wfDoc
	require.NoError(t, yaml.Unmarshal(b, &d), path)
	return d
}

// reusableContract is the required surface of a vendored reusable workflow.
type reusableContract struct {
	hasCall  bool
	inputs   map[string]bool // name -> required (and no default)
	secrets  map[string]bool // name -> required
	required []string
}

func loadContract(t *testing.T, file string) reusableContract {
	t.Helper()
	c := reusableContract{inputs: map[string]bool{}, secrets: map[string]bool{}}
	b, err := os.ReadFile(filepath.Join(callersFixtureDir, file))
	require.NoError(t, err, "no vendored contract for reusable workflow %s: vendor its on: block under %s",
		file, callersFixtureDir)
	var d struct {
		On map[string]any `yaml:"on"`
	}
	require.NoError(t, yaml.Unmarshal(b, &d), file)
	call, ok := d.On["workflow_call"]
	if !ok {
		return c
	}
	c.hasCall = true
	spec, _ := call.(map[string]any)
	for name, v := range asMap(spec["inputs"]) {
		m := asMap(v)
		_, hasDefault := m["default"]
		c.inputs[name] = m["required"] == true && !hasDefault
	}
	for name, v := range asMap(spec["secrets"]) {
		c.secrets[name] = asMap(v)["required"] == true
	}
	for n, req := range c.inputs {
		if req {
			c.required = append(c.required, "input "+n)
		}
	}
	for n, req := range c.secrets {
		if req {
			c.required = append(c.required, "secret "+n)
		}
	}
	sort.Strings(c.required)
	return c
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func renderedCliGo(t *testing.T) (string, Summary) {
	t.Helper()
	in := e2eInputs("callers", false)
	in.WithReleasePlease = true
	return e2eBootstrap(t, in)
}

func workflowFiles(t *testing.T, target string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(target, ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	return files
}

// (a) + (b): every hop-top/.github call targets a workflow with
// workflow_call and passes everything it requires.
func TestCallersContract_EveryReusableCallSatisfiesItsContract(t *testing.T) {
	target, _ := renderedCliGo(t)
	calls := 0
	for _, wf := range workflowFiles(t, target) {
		doc := loadWF(t, wf)
		for name, job := range doc.Jobs {
			m := dotgithubUsesRe.FindStringSubmatch(job.Uses)
			if m == nil {
				continue
			}
			calls++
			where := filepath.Base(wf) + " job " + name + " -> " + m[1]
			c := loadContract(t, m[1])
			require.True(t, c.hasCall, "%s: reusable workflow declares no on.workflow_call, so the run fails", where)

			for in, req := range c.inputs {
				if req {
					assert.Contains(t, job.With, in, "%s: required input %q not passed", where, in)
				}
			}
			for in := range job.With {
				assert.Contains(t, c.inputs, in, "%s: passes undeclared input %q", where, in)
			}
			if s, ok := job.Secrets.(string); ok {
				assert.Equal(t, "inherit", s, where)
				continue
			}
			passed := asMap(job.Secrets)
			for sec, req := range c.secrets {
				if req {
					assert.Contains(t, passed, sec, "%s: required secret %q not passed", where, sec)
				}
			}
			for sec := range passed {
				assert.Contains(t, c.secrets, sec, "%s: passes undeclared secret %q", where, sec)
			}
		}
	}
	assert.GreaterOrEqual(t, calls, 2, "release-please + preflight callers must both be checked")
}

// The vendored fixtures are the ones workflowCallerRef pins.
func TestCallersContract_FixturesMatchPin(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(callersFixtureDir, "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.Contains(t, string(b), workflowCallerRef, "%s vendored from another commit than workflowCallerRef", f)
	}
}

// The known-broken shapes stay caught: ci.yml has no workflow_call, and
// publish-on-tag.yml requires inputs and a secret a bare caller omits.
func TestCallersContract_FixturesEncodeTheOldFailures(t *testing.T) {
	assert.False(t, loadContract(t, "ci.yml").hasCall)
	c := loadContract(t, "publish-on-tag.yml")
	assert.True(t, c.hasCall)
	assert.Contains(t, c.required, "input ecosystems")
	assert.Contains(t, c.required, "secret GH_MIRROR_PAT")
}

// (c) CI and lint run inline, not through a reusable workflow.
func TestCallersContract_CIAndLintInline(t *testing.T) {
	target, _ := renderedCliGo(t)
	ci := loadWF(t, filepath.Join(target, ".github", "workflows", "ci-go.yml"))
	for _, job := range []string{"lint", "test"} {
		j, ok := ci.Jobs[job]
		require.True(t, ok, "ci-go.yml has no %q job", job)
		assert.Empty(t, j.Uses, "%s must be inline", job)
		assert.NotEmpty(t, j.Steps, "%s must carry steps", job)
	}
	assertAbsent(t, target, ".github/workflows/test-go-caller.yml")
	assertAbsent(t, target, ".github/workflows/release-go-caller.yml")
	// Go-only: nothing publishes through publish.yml.
	assertAbsent(t, target, ".github/workflows/publish.yml")
}

// (d) release-please caller + preflight caller + dependabot.yml.
func TestCallersContract_ReleasePleasePreflightDependabot(t *testing.T) {
	target, sum := renderedCliGo(t)

	rp := loadWF(t, filepath.Join(target, ".github", "workflows", "release-please.yml"))
	assert.Contains(t, rp.Jobs["release-please"].Uses, "/release-please-on-push.yml@")

	const pf = ".github/workflows/release-please-preflight.yml"
	doc := loadWF(t, filepath.Join(target, filepath.FromSlash(pf)))
	require.Contains(t, doc.On, "pull_request")
	require.Contains(t, doc.On, "workflow_dispatch")
	assert.Contains(t, doc.Jobs["preflight"].Uses, "/release-please-preflight.yml@")
	assert.Equal(t, "write", mustAction(t, sum.Workflows, pf).Action)

	var dbot struct {
		Version int `yaml:"version"`
		Groups  map[string]struct {
			Schedule map[string]any `yaml:"schedule"`
			Limit    int            `yaml:"open-pull-requests-limit"`
		} `yaml:"multi-ecosystem-groups"`
		Updates []struct {
			Ecosystem string         `yaml:"package-ecosystem"`
			Group     string         `yaml:"multi-ecosystem-group"`
			Cooldown  map[string]any `yaml:"cooldown"`
		} `yaml:"updates"`
	}
	b, err := os.ReadFile(filepath.Join(target, ".github", "dependabot.yml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(b, &dbot))
	assert.Equal(t, 2, dbot.Version)
	assert.Equal(t, 1, dbot.Groups["dependencies"].Limit, "org policy: one version-update PR at a time")
	eco := map[string]bool{}
	for _, u := range dbot.Updates {
		eco[u.Ecosystem] = true
		if u.Ecosystem != "github-actions" { // shares lib.sh's flat entry
			assert.Equal(t, "dependencies", u.Group, u.Ecosystem)
		}
		assert.Equal(t, 14, u.Cooldown["default-days"], "%s: org policy is a 14d cooldown", u.Ecosystem)
	}
	assert.True(t, eco["gomod"] && eco["github-actions"], "%v", eco)
}

// (e) .kit/generated.json lists the files kit now owns, so a rerun
// refreshes them.
func TestCallersContract_ManifestListsRenderedFiles(t *testing.T) {
	target, _ := renderedCliGo(t)
	for _, rel := range []string{
		".github/workflows/release-please.yml",
		".github/workflows/release-please-preflight.yml",
	} {
		assert.True(t, manifestHas(t, target, rel), "manifest must list %s", rel)
	}
	assert.False(t, manifestHas(t, target, ".github/workflows/test-go-caller.yml"))
}

// A repo scaffolded by an earlier kit holds the broken caller in its
// manifest; the next run removes it while unedited and reports it once
// edited.
func TestCallersContract_BrokenTestCallerRetired(t *testing.T) {
	const rel = ".github/workflows/test-go-caller.yml"
	old := renderWorkflowCaller(workflowSpec{OutFile: "test-go-caller.yml", Upstream: "ci.yml", Trigger: "test"})
	seed := func(t *testing.T, body string) string {
		target := t.TempDir()
		writeRel(t, target, rel, body)
		m := &Manifest{Version: manifestVersion, GeneratedBy: manifestGeneratedBy, Files: []ManifestEntry{{
			Path: rel, SHA256: sha256Hex([]byte(old)), GeneratedAt: "2026-01-01T00:00:00Z",
		}}}
		require.NoError(t, writeWorkflowManifest(filepath.Join(target, filepath.FromSlash(manifestRelPath)), m))
		return target
	}

	target := seed(t, old)
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	a := mustAction(t, actions, rel)
	assert.Equal(t, "remove", a.Action)
	assert.Contains(t, a.Detail, "workflow_call")
	assertAbsent(t, target, rel)
	assert.False(t, manifestHas(t, target, rel))

	target = seed(t, old+"# local tweak\n")
	actions, err = renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "keep", mustAction(t, actions, rel).Action)
	assert.True(t, strings.Contains(readRel(t, target, rel), "local tweak"))
}
