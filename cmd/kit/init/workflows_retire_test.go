// Tests for retiring caller stubs kit init no longer renders:
// release-go-caller.yml (a Go module publishes by its bare v<version>
// tag through proxy.golang.org; the caller routed those tags to
// publish-on-tag.yml, which only understands <component>/v<version>).
package kitinit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const retiredGoCaller = ".github/workflows/release-go-caller.yml"

// oldGoReleaseCaller is what earlier kit init versions rendered.
func oldGoReleaseCaller() string {
	return renderWorkflowCaller(workflowSpec{
		OutFile:      "release-go-caller.yml",
		Upstream:     "publish-on-tag.yml",
		Trigger:      "release",
		Notes:        []string{"Publishes Go module tags via the unified hop-top/.github publish-on-tag pipeline."},
		UpstreamTODO: "hop-top/.github exposes the unified publish-on-tag.yml; no dedicated publish-go.yml exists yet. Confirm the input shape with the maintainer before opting in.",
	})
}

// managedOldGoCaller puts the old caller on disk, tracked in the manifest.
func managedOldGoCaller(t *testing.T, target, body string) {
	t.Helper()
	writeRel(t, target, retiredGoCaller, body)
	m := &Manifest{Version: manifestVersion, GeneratedBy: manifestGeneratedBy, Files: []ManifestEntry{{
		Path: retiredGoCaller, SHA256: sha256Hex([]byte(oldGoReleaseCaller())), GeneratedAt: "2026-01-01T00:00:00Z",
	}}}
	require.NoError(t, writeWorkflowManifest(filepath.Join(target, filepath.FromSlash(manifestRelPath)), m))
}

func manifestHas(t *testing.T, target, rel string) bool {
	t.Helper()
	for _, f := range readManifestFile(t, target).Files {
		if f.Path == rel {
			return true
		}
	}
	return false
}

func TestPlanWorkflows_Go_NoReleaseCaller(t *testing.T) {
	var rels []string
	for _, p := range planWorkflows("/tmp/repo", []string{"go", "ts"}) {
		rels = append(rels, p.RelPath)
	}
	assert.NotContains(t, rels, retiredGoCaller)
	assert.Contains(t, rels, ".github/workflows/test-go-caller.yml")
	assert.Contains(t, rels, ".github/workflows/test-ts-caller.yml")
}

func TestRetire_ManagedUnchanged_Removed(t *testing.T) {
	target := t.TempDir()
	managedOldGoCaller(t, target, oldGoReleaseCaller())
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)

	a := mustAction(t, actions, retiredGoCaller)
	assert.Equal(t, "remove", a.Action)
	assert.Equal(t, "retired", a.Reason)
	assert.Contains(t, a.Detail, "proxy.golang.org")
	assertAbsent(t, target, retiredGoCaller)
	assert.False(t, manifestHas(t, target, retiredGoCaller))

	// Second run: nothing left to retire.
	actions, err = renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	_, ok := findAction(actions, retiredGoCaller)
	assert.False(t, ok, "%+v", actions)
}

func TestRetire_ManagedEdited_KeptAndReported(t *testing.T) {
	target := t.TempDir()
	edited := oldGoReleaseCaller() + "# local tweak\n"
	managedOldGoCaller(t, target, edited)
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)

	a := mustAction(t, actions, retiredGoCaller)
	assert.Equal(t, "keep", a.Action)
	assert.Equal(t, "retired-user-edited", a.Reason)
	assert.Contains(t, a.Detail, "delete")
	assert.Equal(t, edited, readRel(t, target, retiredGoCaller))
	assert.True(t, manifestHas(t, target, retiredGoCaller), "still reported next run")
}

func TestRetire_Untracked_LeftAlone(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, retiredGoCaller, "name: mine\n")
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	_, ok := findAction(actions, retiredGoCaller)
	assert.False(t, ok)
	assert.Equal(t, "name: mine\n", readRel(t, target, retiredGoCaller))
}

func TestRetire_AlreadyDeleted_DropsManifestEntry(t *testing.T) {
	target := t.TempDir()
	managedOldGoCaller(t, target, oldGoReleaseCaller())
	require.NoError(t, os.Remove(filepath.Join(target, filepath.FromSlash(retiredGoCaller))))
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	a := mustAction(t, actions, retiredGoCaller)
	assert.Equal(t, "manifest-update", a.Action)
	assert.Equal(t, "retired", a.Reason)
	assert.False(t, manifestHas(t, target, retiredGoCaller))
}

func TestRetire_DryRun_ReportsOnly(t *testing.T) {
	target := t.TempDir()
	managedOldGoCaller(t, target, oldGoReleaseCaller())
	actions, err := renderWorkflows(target, []string{"go"}, Inputs{DryRun: true}, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "remove", mustAction(t, actions, retiredGoCaller).Action)
	assert.Equal(t, oldGoReleaseCaller(), readRel(t, target, retiredGoCaller))
	assert.True(t, manifestHas(t, target, retiredGoCaller))
}

// The caller is obsolete whatever runtimes this run selects.
func TestRetire_WithoutGoRuntime(t *testing.T) {
	target := t.TempDir()
	managedOldGoCaller(t, target, oldGoReleaseCaller())
	actions, err := renderWorkflows(target, []string{"ts"}, Inputs{}, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "remove", mustAction(t, actions, retiredGoCaller).Action)
	assertAbsent(t, target, retiredGoCaller)

	target = t.TempDir()
	managedOldGoCaller(t, target, oldGoReleaseCaller())
	actions, err = renderWorkflows(target, nil, Inputs{}, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "remove", mustAction(t, actions, retiredGoCaller).Action)
}
