// Tests for the single `.github/workflows/publish.yml` kit init renders
// (publish_workflow.go): one caller of hop-top/.github's publish-on-tag.yml
// whose ecosystems map is keyed by the release-please components, in
// place of the per-runtime release-<lang>-caller.yml stubs.
package kitinit

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const publishRel = ".github/workflows/publish.yml"

func publishInputs(runtimes ...string) Inputs {
	return Inputs{Name: "demo", Org: "acme", Runtime: runtimes, WithGitHubWorkflows: true}
}

type publishDoc struct {
	On          map[string]any
	Permissions map[string]any
	Job         map[string]any
	With        map[string]any
	Secrets     map[string]any
	Ecosystems  map[string]map[string]any
}

func parsePublish(t *testing.T, content string) publishDoc {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &doc), content)
	job := doc["jobs"].(map[string]any)["publish"].(map[string]any)
	with := job["with"].(map[string]any)
	var eco map[string]map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(with["ecosystems"].(string)), &eco), with["ecosystems"])
	perms, _ := job["permissions"].(map[string]any)
	return publishDoc{
		On: doc["on"].(map[string]any), Permissions: perms, Job: job, With: with,
		Secrets: job["secrets"].(map[string]any), Ecosystems: eco,
	}
}

func renderPublishFor(t *testing.T, in Inputs) (string, []WorkflowAction) {
	t.Helper()
	target := t.TempDir()
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	return target, actions
}

// --- which repos get one -----------------------------------------------------

func TestPublish_PureGo_None(t *testing.T) {
	target, actions := renderPublishFor(t, publishInputs("go"))
	assertAbsent(t, target, publishRel)
	_, ok := findAction(actions, publishRel)
	assert.False(t, ok)
}

func TestPublish_NoReleaseCallersPlanned(t *testing.T) {
	for _, p := range planWorkflows("/tmp/repo", []string{"go", "ts", "py", "rs", "php"}) {
		assert.NotRegexp(t, `release-[a-z]+-caller\.yml$`, p.RelPath)
	}
}

// --- shape ---------------------------------------------------------------------

func TestPublish_SingleTS(t *testing.T) {
	target, actions := renderPublishFor(t, publishInputs("ts"))
	d := parsePublish(t, readRel(t, target, publishRel))

	assert.Equal(t, map[string]any{"tags": []any{"*/v*"}}, d.On["push"])
	assert.Contains(t, d.On, "workflow_dispatch")
	assert.Equal(t, map[string]any{"contents": "read", "id-token": "write"}, d.Permissions)
	assert.Equal(t, "hop-top/.github/.github/workflows/publish-on-tag.yml@"+workflowCallerRef, d.Job["uses"])
	assert.Equal(t, map[string]any{
		"NPM_REGISTRY_TOKEN": "${{ secrets.NPM_REGISTRY_TOKEN }}",
		"GH_MIRROR_PAT":      "${{ secrets.GH_MIRROR_PAT }}",
	}, d.Secrets)
	assert.Equal(t, false, d.With["enable-mirror"], "single-language ts has no mirror repo")
	assert.Equal(t, "https://github.com/acme/demo", d.With["homepage"])
	assert.Equal(t, map[string]map[string]any{
		"demo": {"dir": ".", "ecosystem": "ts", "package": "@acme/demo"},
	}, d.Ecosystems)

	a := mustAction(t, actions, publishRel)
	assert.Equal(t, "write", a.Action)
	assert.Contains(t, a.Detail, "package names")
}

func TestPublish_SinglePHP_KeepsMirror(t *testing.T) {
	// publish-on-tag.yml fails a php publish with the mirror disabled:
	// Packagist is notified by the mirror job.
	target, _ := renderPublishFor(t, publishInputs("php"))
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.NotContains(t, d.With, "enable-mirror")
	assert.Equal(t, map[string]map[string]any{
		"demo": {"dir": ".", "ecosystem": "php", "package": "acme/demo", "mirror": "acme/demo-php"},
	}, d.Ecosystems)
	assert.Equal(t, map[string]any{
		"PACKAGIST_USERNAME": "${{ secrets.PACKAGIST_USERNAME }}",
		"PACKAGIST_TOKEN":    "${{ secrets.PACKAGIST_TOKEN }}",
		"GH_MIRROR_PAT":      "${{ secrets.GH_MIRROR_PAT }}",
	}, d.Secrets)
}

func TestPublish_Polyglot(t *testing.T) {
	target, _ := renderPublishFor(t, publishInputs("go", "ts", "py", "rs", "php"))
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.NotContains(t, d.With, "enable-mirror")
	assert.Equal(t, map[string]map[string]any{
		"demo-ts":  {"dir": "ts", "ecosystem": "ts", "package": "@acme/demo", "mirror": "acme/demo-ts"},
		"demo-py":  {"dir": "py", "ecosystem": "py", "package": "acme-demo", "mirror": "acme/demo-py"},
		"demo-rs":  {"dir": "rs", "ecosystem": "rs", "package": "acme-demo", "mirror": "acme/demo-rs"},
		"demo-php": {"dir": "php", "ecosystem": "php", "package": "acme/demo", "mirror": "acme/demo-php"},
	}, d.Ecosystems, "Go is not listed: its bare v tags publish through proxy.golang.org")
	assert.Equal(t, map[string]any{
		"NPM_REGISTRY_TOKEN":   "${{ secrets.NPM_REGISTRY_TOKEN }}",
		"CARGO_REGISTRY_TOKEN": "${{ secrets.CARGO_REGISTRY_TOKEN }}",
		"PACKAGIST_USERNAME":   "${{ secrets.PACKAGIST_USERNAME }}",
		"PACKAGIST_TOKEN":      "${{ secrets.PACKAGIST_TOKEN }}",
		"GH_MIRROR_PAT":        "${{ secrets.GH_MIRROR_PAT }}",
	}, d.Secrets)
}

// The ecosystems keys must be the release-please components the starter
// config tags with, or publish-on-tag.yml finds no entry for a tag.
func TestPublish_KeysMatchStarterConfigComponents(t *testing.T) {
	for _, rts := range [][]string{{"ts"}, {"py"}, {"go", "ts", "py"}, {"ts", "rs", "php"}} {
		in := publishInputs(rts...)
		target, _ := renderPublishFor(t, in)
		d := parsePublish(t, readRel(t, target, publishRel))
		cfg, _ := renderReleasePleaseSeed(in.Name, rts)
		c, _ := decodeSeed(t, cfg, []byte("{}"))
		want := map[string]string{}
		for path, p := range c.Packages {
			if p["release-type"] != "go" {
				want[p["component"].(string)] = path
			}
		}
		got := map[string]string{}
		for comp, e := range d.Ecosystems {
			got[comp] = e["dir"].(string)
		}
		assert.Equal(t, want, got, "%v", rts)
	}
}

// An existing release-please config is the source of truth for
// components and directories.
func TestPublish_FromExistingConfig(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, ".github/release-please-config.json", `{
	  "packages": {
	    ".": {"release-type": "go", "component": "kit"},
	    "sdk/ts": {"release-type": "node", "component": "kit-ts"},
	    "sdk/py": {"release-type": "python", "component": "kit-py"},
	    "spec": {"release-type": "simple", "component": "spec"}
	  }
	}`)
	in := publishInputs("go")
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.Equal(t, map[string]map[string]any{
		"kit-ts": {"dir": "sdk/ts", "ecosystem": "ts", "package": "@acme/kit", "mirror": "acme/kit-ts"},
		"kit-py": {"dir": "sdk/py", "ecosystem": "py", "package": "acme-kit", "mirror": "acme/kit-py"},
	}, d.Ecosystems)
}

// --- owner resolution ------------------------------------------------------------

func TestPublish_OwnerFromModule(t *testing.T) {
	in := publishInputs("ts")
	in.Org = ""
	in.Module = "github.com/someone/demo"
	target, _ := renderPublishFor(t, in)
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.Equal(t, "@someone/demo", d.Ecosystems["demo"]["package"])
}

func TestPublish_OwnerFromOriginRemote(t *testing.T) {
	target := gitRepoWith(t, map[string]string{"README.md": "x\n"})
	rpGit(t, target, "remote", "add", "origin", "git@github.com:remote-org/demo.git")
	in := publishInputs("ts")
	in.Org = ""
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.Equal(t, "@remote-org/demo", d.Ecosystems["demo"]["package"])
}

func TestPublish_OwnerUnknown_PlaceholderFlagged(t *testing.T) {
	in := publishInputs("ts", "py")
	in.Org = ""
	target, actions := renderPublishFor(t, in)
	d := parsePublish(t, readRel(t, target, publishRel))
	assert.Equal(t, "OWNER/demo-ts", d.Ecosystems["demo-ts"]["mirror"])
	assert.NotContains(t, d.With, "homepage")
	assert.Contains(t, mustAction(t, actions, publishRel).Detail, "OWNER")
}

// --- existing publish wiring -------------------------------------------------------

// A workflow already publishing through hop-top/.github keeps publishing;
// publish.yml is only suggested, so no tag publishes twice.
func TestPublish_ExistingPublisherElsewhere_SuggestsOnly(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, ".github/workflows/release.yml", `name: release
on:
  push:
    tags: ['*/v*']
jobs:
  publish:
    uses: hop-top/.github/.github/workflows/publish-on-tag.yml@v0
    secrets: inherit
`)
	in := publishInputs("ts")
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assertAbsent(t, target, publishRel)
	a := mustAction(t, actions, publishRel)
	assert.Equal(t, "suggest-sibling", a.Action)
	assert.Equal(t, "existing-publisher", a.Reason)
	assert.Contains(t, a.Detail, ".github/workflows/release.yml")
	assert.FileExists(t, target+"/"+publishRel+suggestedSuffix)
}

func TestPublish_HandWrittenPublishYml_Untouched(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, publishRel, "name: publish\n# mine\n")
	in := publishInputs("ts")
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "name: publish\n# mine\n", readRel(t, target, publishRel))
	assert.Equal(t, "suggest-sibling", mustAction(t, actions, publishRel).Action)
}

func TestPublish_Rerun_SkipsUnchanged(t *testing.T) {
	target := t.TempDir()
	in := publishInputs("go", "ts")
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	before := readRel(t, target, publishRel)
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "skip-unchanged", mustAction(t, actions, publishRel).Action)
	assert.Equal(t, before, readRel(t, target, publishRel))
}

// --- retiring the per-runtime release callers --------------------------------------

func TestPublish_RetiresManagedReleaseCaller_ThenWritesLive(t *testing.T) {
	target := t.TempDir()
	old := renderWorkflowCaller(workflowSpec{
		OutFile: "release-ts-caller.yml", Upstream: "publish-ts.yml", Trigger: "release",
		Notes: []string{"Publishes the npm package via the hop-top/.github publish-ts reusable workflow."},
	})
	writeRel(t, target, ".github/workflows/release-ts-caller.yml", old)
	m := &Manifest{Version: manifestVersion, GeneratedBy: manifestGeneratedBy, Files: []ManifestEntry{{
		Path: ".github/workflows/release-ts-caller.yml", SHA256: sha256Hex([]byte(old)), GeneratedAt: "2026-01-01T00:00:00Z",
	}}}
	require.NoError(t, writeWorkflowManifest(target+"/"+manifestRelPath, m))

	in := publishInputs("ts")
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	r := mustAction(t, actions, ".github/workflows/release-ts-caller.yml")
	assert.Equal(t, "remove", r.Action)
	assert.Contains(t, r.Detail, "publish.yml")
	assertAbsent(t, target, ".github/workflows/release-ts-caller.yml")
	assert.Equal(t, "write", mustAction(t, actions, publishRel).Action, "retired caller no longer publishes")

	// Dry-run projects the same outcome.
	target2 := t.TempDir()
	writeRel(t, target2, ".github/workflows/release-ts-caller.yml", old)
	require.NoError(t, writeWorkflowManifest(target2+"/"+manifestRelPath, m))
	in.DryRun = true
	actions, err = renderWorkflows(target2, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "write", mustAction(t, actions, publishRel).Action)
}

func TestPublish_EditedRetiredCallerStillPublishes_SuggestsOnly(t *testing.T) {
	target := t.TempDir()
	old := renderWorkflowCaller(workflowSpec{OutFile: "release-py-caller.yml", Upstream: "publish-py.yml", Trigger: "release"})
	writeRel(t, target, ".github/workflows/release-py-caller.yml", old+"# tweak\n")
	m := &Manifest{Version: manifestVersion, GeneratedBy: manifestGeneratedBy, Files: []ManifestEntry{{
		Path: ".github/workflows/release-py-caller.yml", SHA256: sha256Hex([]byte(old)), GeneratedAt: "2026-01-01T00:00:00Z",
	}}}
	require.NoError(t, writeWorkflowManifest(target+"/"+manifestRelPath, m))
	in := publishInputs("py")
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "keep", mustAction(t, actions, ".github/workflows/release-py-caller.yml").Action)
	assert.Equal(t, "existing-publisher", mustAction(t, actions, publishRel).Reason)
	assertAbsent(t, target, publishRel)
}

// Once the other publisher is gone, publish.yml goes live and the
// suggestion it replaces is cleaned up in the same run.
func TestPublish_GoesLiveAfterOtherPublisherRemoved_PrunesSibling(t *testing.T) {
	target := t.TempDir()
	other := ".github/workflows/release.yml"
	writeRel(t, target, other, "jobs:\n  p:\n    uses: hop-top/.github/.github/workflows/publish-ts.yml@v0\n")
	in := publishInputs("ts")
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.FileExists(t, target+"/"+publishRel+suggestedSuffix)

	require.NoError(t, os.Remove(target+"/"+other))
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "write", mustAction(t, actions, publishRel).Action)
	assertAbsent(t, target, publishRel+suggestedSuffix)
}
