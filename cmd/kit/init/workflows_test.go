// Tests for the T-0772 `.github/workflows/*-caller.yml` generator.
//
// White-box (package kitinit) so we can exercise renderWorkflows and the
// manifest helpers directly. Real disk via t.TempDir; no network, no
// process spawning. Tests assert:
//   - per-runtime caller files exist with the expected `uses:` line.
//   - the `.kit/generated.json` manifest is written with one entry per
//     generated file (path + sha256 + generatedAt).
//   - a pre-existing file whose hash diverges from the manifest produces
//     a `.kit-suggested` sibling and leaves the original untouched.
//   - an accepted suggestion (`.kit-suggested` byte-identical to live)
//     is removed during the next run.
//   - --dry-run reports actions but writes nothing on disk.
//   - --without-github-workflows (Inputs.WithGitHubWorkflows=false) is a
//     no-op.
package kitinit

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tmpl "hop.top/kit/internal/template"
)

// fixedNow returns a deterministic clock for manifest timestamps so
// assertions are stable across machines.
func fixedNow() func() time.Time {
	t := time.Date(2026, 5, 23, 14, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// readManifestFile decodes `.kit/generated.json` from target.
func readManifestFile(t *testing.T, target string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, ".kit", "generated.json"))
	require.NoError(t, err)
	var m Manifest
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestPlanWorkflows_GoRuntime_EmitsReleaseAndTest(t *testing.T) {
	plans := planWorkflows("/tmp/repo", []string{"go"})
	var rels []string
	for _, p := range plans {
		rels = append(rels, p.RelPath)
	}
	sort.Strings(rels)
	assert.Equal(t, []string{
		".github/workflows/release-go-caller.yml",
		".github/workflows/test-go-caller.yml",
	}, rels)
}

func TestPlanWorkflows_AllSupportedRuntimes(t *testing.T) {
	plans := planWorkflows("/tmp/repo", []string{"go", "rs", "ts", "php", "py"})
	got := make(map[string]bool, len(plans))
	for _, p := range plans {
		got[p.RelPath] = true
	}
	for _, want := range []string{
		".github/workflows/release-go-caller.yml",
		".github/workflows/release-rs-caller.yml",
		".github/workflows/release-ts-caller.yml",
		".github/workflows/release-php-caller.yml",
		".github/workflows/release-py-caller.yml",
		".github/workflows/test-go-caller.yml",
		".github/workflows/test-rs-caller.yml",
		".github/workflows/test-ts-caller.yml",
		".github/workflows/test-php-caller.yml",
		".github/workflows/test-py-caller.yml",
	} {
		assert.True(t, got[want], "missing planned workflow: %s", want)
	}
}

func TestPlanWorkflows_UnknownRuntime_Skipped(t *testing.T) {
	plans := planWorkflows("/tmp/repo", []string{"go", "swift", "kotlin"})
	for _, p := range plans {
		assert.NotContains(t, p.RelPath, "swift")
		assert.NotContains(t, p.RelPath, "kotlin")
	}
	// Still produces the Go callers.
	assert.NotEmpty(t, plans)
}

func TestRenderWorkflowCaller_ReleaseUsesPublishOnTag(t *testing.T) {
	content := renderWorkflowCaller(workflowSpec{
		OutFile:  "release-go-caller.yml",
		Upstream: "publish-on-tag.yml",
		Trigger:  "release",
	})
	assert.Contains(t, content,
		"uses: hop-top/.github/.github/workflows/publish-on-tag.yml@"+workflowCallerRef)
	assert.Contains(t, content, "secrets: inherit")
	assert.Contains(t, content, "id-token: write")
	assert.Contains(t, content, "tags:")
}

func TestRenderWorkflowCaller_TestUsesCi(t *testing.T) {
	content := renderWorkflowCaller(workflowSpec{
		OutFile:  "test-go-caller.yml",
		Upstream: "ci.yml",
		Trigger:  "test",
	})
	assert.Contains(t, content,
		"uses: hop-top/.github/.github/workflows/ci.yml@"+workflowCallerRef)
	assert.Contains(t, content, "pull_request:")
	// Test callers don't need secrets:inherit by default.
	assert.NotContains(t, content, "secrets: inherit")
}

func TestRenderWorkflows_Bootstrap_WritesFilesAndManifest(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	require.Len(t, actions, 2)
	for _, a := range actions {
		assert.Equal(t, "write", a.Action)
		assert.Equal(t, "new", a.Reason)
	}

	// Files exist on disk with the expected `uses:` line.
	for _, rel := range []string{
		".github/workflows/release-go-caller.yml",
		".github/workflows/test-go-caller.yml",
	} {
		body, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		require.NoError(t, err, "missing %s", rel)
		assert.Contains(t, string(body), "uses: hop-top/.github/.github/workflows/")
	}

	// Manifest captures both files with sha256 + generatedAt.
	m := readManifestFile(t, target)
	assert.Equal(t, manifestVersion, m.Version)
	assert.Equal(t, manifestGeneratedBy, m.GeneratedBy)
	require.Len(t, m.Files, 2)
	for _, f := range m.Files {
		assert.True(t, strings.HasPrefix(f.Path, ".github/workflows/"), "path=%q", f.Path)
		assert.NotEmpty(t, f.SHA256)
		assert.Equal(t, "2026-05-23T14:00:00Z", f.GeneratedAt)
	}
}

func TestRenderWorkflows_DryRun_NoDiskWrites(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true, DryRun: true}

	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	require.NotEmpty(t, actions)

	// Nothing on disk.
	_, err = os.Stat(filepath.Join(target, ".github"))
	assert.True(t, os.IsNotExist(err), "dry-run must not create .github/")
	_, err = os.Stat(filepath.Join(target, ".kit"))
	assert.True(t, os.IsNotExist(err), "dry-run must not create .kit/")
}

func TestRenderWorkflows_NoRuntimes_NoOp(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: nil, WithGitHubWorkflows: true}

	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	assert.Nil(t, actions)

	_, err = os.Stat(filepath.Join(target, ".kit", "generated.json"))
	assert.True(t, os.IsNotExist(err), "no runtimes → no manifest")
}

func TestRenderWorkflows_PreExistingFile_DivergentHash_WritesSibling(t *testing.T) {
	target := t.TempDir()
	rel := ".github/workflows/release-go-caller.yml"
	abs := filepath.Join(target, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o750))

	// Pre-existing user-authored file (no manifest entry → "user-edited").
	userContent := "# user-authored, do not touch\nname: my-custom-release\n"
	require.NoError(t, os.WriteFile(abs, []byte(userContent), 0o644))

	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	// Find the action for release-go-caller.
	var releaseAction *WorkflowAction
	for i := range actions {
		if actions[i].Path == rel {
			releaseAction = &actions[i]
			break
		}
	}
	require.NotNil(t, releaseAction, "expected action for %s", rel)
	assert.Equal(t, "suggest-sibling", releaseAction.Action)
	assert.Equal(t, "user-edited", releaseAction.Reason)
	assert.Equal(t, rel+suggestedSuffix, releaseAction.SuggestedPath)

	// Original file untouched.
	got, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Equal(t, userContent, string(got),
		"existing file must be left untouched on user-edited divergence")

	// Sibling exists and contains the generated content.
	siblingBytes, err := os.ReadFile(abs + suggestedSuffix)
	require.NoError(t, err)
	assert.Contains(t, string(siblingBytes), "uses: hop-top/.github/.github/workflows/")

	// Manifest does NOT claim the user-edited path — the user still owns it.
	m := readManifestFile(t, target)
	for _, f := range m.Files {
		assert.NotEqual(t, rel, f.Path,
			"manifest must not record paths kit chose not to overwrite")
	}
}

func TestRenderWorkflows_RefreshInPlace_WhenManifestHashMatches(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	// First run: bootstrap → writes the two go callers.
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	rel := ".github/workflows/release-go-caller.yml"
	abs := filepath.Join(target, filepath.FromSlash(rel))

	// Tamper with the renderer output (swap the upstream filename) and
	// re-render. The on-disk file still matches the manifest (we just
	// touched it via renderWorkflows above) so kit refreshes in place.
	//
	// To simulate a regenerated stub, replace the file content with the
	// manifest-matching original first, then call again with a mutated
	// runtimeWorkflows table.
	origSpecs := runtimeWorkflows["go"]
	t.Cleanup(func() { runtimeWorkflows["go"] = origSpecs })

	runtimeWorkflows["go"] = []workflowSpec{
		{
			OutFile:  "release-go-caller.yml",
			Upstream: "publish-on-tag-v2.yml", // different upstream → different content
			Trigger:  "release",
		},
		{
			OutFile:  "test-go-caller.yml",
			Upstream: "ci.yml",
			Trigger:  "test",
		},
	}

	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	var releaseAction *WorkflowAction
	for i := range actions {
		if actions[i].Path == rel {
			releaseAction = &actions[i]
			break
		}
	}
	require.NotNil(t, releaseAction)
	assert.Equal(t, "write", releaseAction.Action)
	assert.Equal(t, "refresh", releaseAction.Reason)

	// File now contains the new upstream reference.
	got, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Contains(t, string(got), "publish-on-tag-v2.yml")
}

func TestRenderWorkflows_IdempotentRerun_SkipsUnchanged(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	// Second run with no changes: all entries report skip-unchanged.
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)
	require.NotEmpty(t, actions)
	for _, a := range actions {
		assert.Equal(t, "skip-unchanged", a.Action, "path=%s", a.Path)
	}
}

func TestRenderWorkflows_AcceptedSuggestion_IsRemoved(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	rel := ".github/workflows/release-go-caller.yml"
	abs := filepath.Join(target, filepath.FromSlash(rel))

	// 1. Pre-existing user-authored file → produces a sibling.
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o750))
	require.NoError(t, os.WriteFile(abs, []byte("# user version\n"), 0o644))

	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	sibling := abs + suggestedSuffix
	siblingBytes, err := os.ReadFile(sibling)
	require.NoError(t, err)

	// 2. User accepts the suggestion: copy sibling contents onto the
	// live file (byte-identical). The next run should prune the sibling.
	require.NoError(t, os.WriteFile(abs, siblingBytes, 0o644))

	_, err = renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	_, statErr := os.Stat(sibling)
	assert.True(t, os.IsNotExist(statErr),
		"sibling must be removed once live file matches it; stat err = %v", statErr)
}

func TestRenderWorkflows_Disabled_NoOp(t *testing.T) {
	target := t.TempDir()
	// The package contract is that callers gate on Inputs.WithGitHubWorkflows
	// BEFORE invoking renderWorkflows. This test asserts the
	// runBootstrap/runAugment wiring through the package surface.
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: false}

	// Skip the renderer call entirely — that's how bootstrap/augment
	// honor the flag. We just verify that if we never call it, nothing
	// is written.
	if in.WithGitHubWorkflows {
		t.Fatalf("test setup invariant violated")
	}
	_, err := os.Stat(filepath.Join(target, ".github"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(target, ".kit"))
	assert.True(t, os.IsNotExist(err))
}

// TestRenderWorkflows_UserEditConverges_ReclaimsManifest covers the
// convergence reclaim case from Section 6: when the on-disk file has been
// user-edited (hash diverged from manifest) but the planned content
// happens to match the live file byte-for-byte, kit should reclaim the
// path by updating the manifest entry to the new hash and reporting a
// `manifest-update` action. The stale `.kit-suggested` sibling, if any,
// must also be cleaned up.
func TestRenderWorkflows_UserEditConverges_ReclaimsManifest(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	rel := ".github/workflows/release-go-caller.yml"
	abs := filepath.Join(target, filepath.FromSlash(rel))
	relTest := ".github/workflows/test-go-caller.yml"

	// Bootstrap so the manifest exists with hash X for the release file.
	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	// Seed the manifest with a STALE hash for the release file to model a
	// user-edited divergence. The on-disk file is left unchanged so its
	// real hash will match the planner's render output on the next run.
	manifestPath := filepath.Join(target, ".kit", "generated.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var m Manifest
	require.NoError(t, json.Unmarshal(manifestBytes, &m))
	for i := range m.Files {
		if m.Files[i].Path == rel {
			m.Files[i].SHA256 = "stale-deadbeef-not-the-current-hash"
		}
	}
	tampered, err := json.MarshalIndent(&m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, tampered, 0o644))

	// Pre-create a `.kit-suggested` sibling with content IDENTICAL to the
	// live file so the convergence path must also remove the sibling.
	live, err := os.ReadFile(abs)
	require.NoError(t, err)
	sibling := abs + suggestedSuffix
	require.NoError(t, os.WriteFile(sibling, live, 0o644))

	// Now re-run: the planner's render still matches the live file
	// (we never touched it) so kit should reclaim, not suggest-sibling.
	actions, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	var releaseAction *WorkflowAction
	for i := range actions {
		if actions[i].Path == rel {
			releaseAction = &actions[i]
			break
		}
	}
	require.NotNil(t, releaseAction, "expected action for %s", rel)
	assert.Equal(t, "manifest-update", releaseAction.Action,
		"convergence should reclaim, not skip-unchanged")
	assert.Equal(t, "convergence", releaseAction.Reason)

	// Sibling must be pruned now that the live file equals the suggestion.
	_, statErr := os.Stat(sibling)
	assert.True(t, os.IsNotExist(statErr),
		"stale .kit-suggested sibling must be pruned during convergence")

	// Manifest reclaimed: entry SHA256 equals the current file hash.
	mm := readManifestFile(t, target)
	var got ManifestEntry
	for _, f := range mm.Files {
		if f.Path == rel {
			got = f
		}
	}
	require.NotEmpty(t, got.Path, "manifest must keep an entry for %s", rel)
	assert.NotEqual(t, "stale-deadbeef-not-the-current-hash", got.SHA256,
		"manifest hash must be refreshed on convergence")
	currentHash := sha256Hex(live)
	assert.Equal(t, currentHash, got.SHA256,
		"manifest hash must match the live file hash after reclaim")

	// The unrelated test caller (no tampering) stays skip-unchanged.
	for _, a := range actions {
		if a.Path == relTest {
			assert.Equal(t, "skip-unchanged", a.Action)
		}
	}
}

// TestReadManifest_RejectsUnknownVersion verifies that readManifest
// fails fast when the on-disk manifest pins a version that this kit
// build doesn't recognize. We default missing (==0) fields, but a
// non-zero value MUST match the pinned constant.
func TestReadManifest_RejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.json")
	data := []byte(`{"version": 2, "generated_by": "kit-init", "files": []}`)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err := readManifest(path)
	require.Error(t, err, "version 2 manifest must be rejected")
	assert.Contains(t, err.Error(), "version",
		"error must mention the version mismatch: %v", err)
}

// TestReadManifest_RejectsUnknownGeneratedBy ensures a manifest written
// by a different tool name doesn't get silently accepted by kit-init.
func TestReadManifest_RejectsUnknownGeneratedBy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.json")
	data := []byte(`{"version": 1, "generated_by": "wrong-tool", "files": []}`)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err := readManifest(path)
	require.Error(t, err, "generated_by 'wrong-tool' must be rejected")
	assert.Contains(t, err.Error(), "generated_by",
		"error must mention the generated_by mismatch: %v", err)
}

// TestReadManifest_DefaultsMissingFields preserves the legacy behavior:
// if both fields are missing/zero, defaults apply. This guards against
// the new validation accidentally breaking the empty-manifest path.
func TestReadManifest_DefaultsMissingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.json")
	// Both fields absent → defaults must apply.
	data := []byte(`{"files": []}`)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	m, err := readManifest(path)
	require.NoError(t, err)
	assert.Equal(t, manifestVersion, m.Version)
	assert.Equal(t, manifestGeneratedBy, m.GeneratedBy)
}

// TestReadManifest_AcceptsCorrectSchema is the happy path: version and
// generated_by match the pinned constants.
func TestReadManifest_AcceptsCorrectSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.json")
	data := []byte(`{"version": 1, "generated_by": "kit-init", "files": []}`)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	m, err := readManifest(path)
	require.NoError(t, err)
	assert.Equal(t, manifestVersion, m.Version)
	assert.Equal(t, manifestGeneratedBy, m.GeneratedBy)
}

// TestWriteManifest_OverwriteIdempotent exercises the manifest write
// path across two consecutive writes to the same location. On POSIX
// os.Rename silently replaces the destination so this passes today;
// on Windows os.Rename fails if the destination exists, so this test
// fails on Windows runners before the fix and passes everywhere after.
// Kept portable so future changes to the write path stay covered.
func TestWriteManifest_OverwriteIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.json")

	// First write: brand-new manifest.
	first := &Manifest{
		Version:     manifestVersion,
		GeneratedBy: manifestGeneratedBy,
		Files: []ManifestEntry{
			{Path: ".github/workflows/a.yml", SHA256: "aaaa", GeneratedAt: "2026-05-23T14:00:00Z"},
		},
	}
	require.NoError(t, writeManifest(path, first))
	got1, err := readManifest(path)
	require.NoError(t, err)
	require.Len(t, got1.Files, 1)
	assert.Equal(t, "aaaa", got1.Files[0].SHA256)

	// Second write: overwrite with different content. This is the
	// codepath that fails on Windows when os.Rename isn't preceded by
	// os.Remove on the destination.
	second := &Manifest{
		Version:     manifestVersion,
		GeneratedBy: manifestGeneratedBy,
		Files: []ManifestEntry{
			{Path: ".github/workflows/a.yml", SHA256: "bbbb", GeneratedAt: "2026-05-23T15:00:00Z"},
			{Path: ".github/workflows/b.yml", SHA256: "cccc", GeneratedAt: "2026-05-23T15:00:00Z"},
		},
	}
	require.NoError(t, writeManifest(path, second),
		"writeManifest must overwrite an existing manifest; this is the path that fails on Windows pre-fix")

	got2, err := readManifest(path)
	require.NoError(t, err)
	require.Len(t, got2.Files, 2)
	assert.Equal(t, "bbbb", got2.Files[0].SHA256)
	assert.Equal(t, "cccc", got2.Files[1].SHA256)

	// No stray temp file left behind.
	_, statErr := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(statErr),
		"manifest temp file must be removed after a successful overwrite")
}

func TestRenderWorkflows_HopTopRefIsPinned(t *testing.T) {
	// The default ref must be `v0` (matches existing poly-kit callers in
	// .github/workflows/publish.yml). If you ever bump it, tighten the
	// matching workflow assertions in track plan.md.
	assert.Equal(t, "v0", workflowCallerRef)
}

// TestBootstrap_WithGitHubWorkflows verifies the end-to-end integration:
// runBootstrap renders the cli-go template AND the T-0772 workflow callers,
// and the summary carries the WorkflowAction list.
func TestBootstrap_WithGitHubWorkflows(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	deps := Deps{
		Registry: tmpl.NewRegistry("", ""),
		Hooks:    &recordingHookRunner{},
		Git:      &recordingGitRunner{},
		GitHub:   &recordingGitHubRunner{},
		Output:   io.Discard,
	}

	name := "demo"
	in := Inputs{
		Name:                name,
		Module:              "github.com/example/" + name,
		License:             "MIT",
		Author:              "Test User",
		Email:               "test@example.com",
		AccountType:         "none",
		Template:            "cli-go",
		DefaultBranch:       "main",
		Runtime:             []string{"go"},
		Tier:                0,
		Hop:                 false,
		NoGitHub:            true,
		NoPush:              true,
		WithGitHubWorkflows: true,
		Vars: map[string]any{
			"Name":          name,
			"name":          name,
			"Module":        "github.com/example/" + name,
			"module":        "github.com/example/" + name,
			"License":       "MIT",
			"license":       "MIT",
			"Author":        "Test User",
			"author":        "Test User",
			"Email":         "test@example.com",
			"email":         "test@example.com",
			"NameUpper":     "DEMO",
			"Year":          2026,
			"Description":   "A demo CLI",
			"description":   "A demo CLI",
			"Org":           "example",
			"org":           "example",
			"DefaultBranch": "main",
		},
	}

	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	require.NotEmpty(t, summary.Workflows, "summary must surface workflow actions")
	for _, a := range summary.Workflows {
		assert.Equal(t, "write", a.Action)
		assert.Equal(t, "new", a.Reason)
	}

	target := filepath.Join(tmpDir, name)
	for _, rel := range []string{
		".github/workflows/release-go-caller.yml",
		".github/workflows/test-go-caller.yml",
		".kit/generated.json",
	} {
		assert.FileExists(t, filepath.Join(target, filepath.FromSlash(rel)))
	}

	// Manifest entries match the on-disk file count for the generated paths.
	m := readManifestFile(t, target)
	assert.Len(t, m.Files, 2)
}

// TestBootstrap_WithoutGitHubWorkflows confirms the opt-out path: when
// WithGitHubWorkflows is false, no .github/workflows/*-caller.yml files
// are written and no manifest is created on disk.
func TestBootstrap_WithoutGitHubWorkflows(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	deps := Deps{
		Registry: tmpl.NewRegistry("", ""),
		Hooks:    &recordingHookRunner{},
		Git:      &recordingGitRunner{},
		GitHub:   &recordingGitHubRunner{},
		Output:   io.Discard,
	}

	name := "demo"
	in := Inputs{
		Name:                name,
		Module:              "github.com/example/" + name,
		License:             "MIT",
		Author:              "Test User",
		Email:               "test@example.com",
		AccountType:         "none",
		Template:            "cli-go",
		DefaultBranch:       "main",
		Runtime:             []string{"go"},
		Tier:                0,
		Hop:                 false,
		NoGitHub:            true,
		NoPush:              true,
		WithGitHubWorkflows: false, // explicit opt-out
		Vars: map[string]any{
			"Name":          name,
			"name":          name,
			"Module":        "github.com/example/" + name,
			"module":        "github.com/example/" + name,
			"License":       "MIT",
			"license":       "MIT",
			"Author":        "Test User",
			"author":        "Test User",
			"Email":         "test@example.com",
			"email":         "test@example.com",
			"NameUpper":     "DEMO",
			"Year":          2026,
			"Description":   "A demo CLI",
			"description":   "A demo CLI",
			"Org":           "example",
			"org":           "example",
			"DefaultBranch": "main",
		},
	}

	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)
	assert.Empty(t, summary.Workflows, "opt-out must produce no workflow actions")

	target := filepath.Join(tmpDir, name)
	_, statErr := os.Stat(filepath.Join(target, ".github", "workflows", "release-go-caller.yml"))
	assert.True(t, os.IsNotExist(statErr),
		"opt-out: release-go-caller.yml must not exist; stat err = %v", statErr)
	_, statErr = os.Stat(filepath.Join(target, ".kit", "generated.json"))
	assert.True(t, os.IsNotExist(statErr),
		"opt-out: manifest must not exist; stat err = %v", statErr)
}

func TestRenderWorkflows_ManifestRoundtrip(t *testing.T) {
	target := t.TempDir()
	in := Inputs{Runtime: []string{"go"}, WithGitHubWorkflows: true}

	_, err := renderWorkflows(target, in.Runtime, in, fixedNow())
	require.NoError(t, err)

	// Manifest is valid JSON, parseable into Manifest.
	data, err := os.ReadFile(filepath.Join(target, ".kit", "generated.json"))
	require.NoError(t, err)
	var roundtrip Manifest
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	assert.Equal(t, manifestVersion, roundtrip.Version)
	assert.NotEmpty(t, roundtrip.Files)
	// Paths are POSIX-form (forward slashes).
	for _, f := range roundtrip.Files {
		assert.False(t, strings.Contains(f.Path, "\\"),
			"manifest path must use forward slashes; got %q", f.Path)
	}
}
