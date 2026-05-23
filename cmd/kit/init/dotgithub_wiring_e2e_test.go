// dotgithub_wiring_e2e_test.go — cross-generator e2e coverage for the
// kit-init-dotgithub-wiring track (T-0775).
//
// This file is the integration counterpart to the per-generator unit tests
// already shipped by T-0772 (workflows_test.go), T-0773 (prepr_test.go),
// T-0774 (posthook_test.go, posthook_ps1_test.go), and T-0776
// (buswf/*_test.go + bus_workflows_test.go). Where those tests verify each
// generator in isolation, this file drives the full bootstrap and augment
// flows end-to-end and asserts the four generator families compose
// coherently:
//
//   - .github/workflows/*-caller.yml (workflow callers — T-0772)
//   - .githooks/pre-pr{,.ps1}        (before-PR hook — T-0773)
//   - .githooks/post-pr-open{,.ps1}  (after-PR hook — T-0774)
//   - .github/workflows/kit-bus-*.yml (bus event workflows — T-0776, opt-in)
//
// Contract: docs/contracts/kit-init-pr-wiring.md (Sections 1, 2, 3, 5, 6, 8).
//
// White-box (package kitinit) so we can drive runBootstrap / runAugment
// directly with the recording runners declared in testhelpers_test.go and
// re-use the embedded asset content for hash assertions. No subprocess,
// no network — every assertion targets observable on-disk state and the
// returned Summary.
package kitinit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"hop.top/kit/cmd/kit/init/buswf"
	"hop.top/kit/go/runtime/bus"
	tmpl "hop.top/kit/internal/template"
)

// -----------------------------------------------------------------------------
// Shared scaffolding.
// -----------------------------------------------------------------------------

// e2eInputs builds an Inputs struct that bootstraps a minimal cli-go
// project with every generator's --with-* flag set per task done-when
// (workflows + pre-pr + post-pr-open ON, bus workflows OFF unless caller
// flips withBus).
func e2eInputs(name string, withBus bool) Inputs {
	return Inputs{
		Name:                  name,
		Module:                "github.com/example/" + name,
		License:               "MIT",
		Author:                "Test User",
		Email:                 "test@example.com",
		AccountType:           "none",
		Theme:                 "daylight",
		Template:              "cli-go",
		Description:           "demo",
		DefaultBranch:         "main",
		Runtime:               []string{"go"},
		Tier:                  0,
		Hop:                   false,
		NoGitHub:              true,
		NoPush:                true,
		WithGitHubWorkflows:   true,
		WithPrePrHook:         true,
		WithGithookPostPROpen: true,
		WithBusWorkflows:      withBus,
		Vars: map[string]any{
			"Name":          name,
			"name":          name,
			"Module":        "github.com/example/" + name,
			"module":        "github.com/example/" + name,
			"License":       "MIT",
			"Author":        "Test User",
			"Email":         "test@example.com",
			"NameUpper":     strings.ToUpper(name),
			"Year":          2026,
			"Description":   "demo",
			"Org":           "example",
			"DefaultBranch": "main",
		},
	}
}

// e2eDeps returns a Deps with the recording runners declared in
// testhelpers_test.go so runBootstrap never shells out.
func e2eDeps() Deps {
	return Deps{
		Registry: tmpl.NewRegistry("", ""),
		Hooks:    &recordingHookRunner{},
		Git:      &recordingGitRunner{},
		GitHub:   &recordingGitHubRunner{},
		Output:   io.Discard,
	}
}

// sha256Of returns the lowercase hex SHA-256 of b. Distinct from the
// in-package sha256Hex helper to keep the e2e file self-contained
// against future internal refactors.
func sha256Of(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// e2eBootstrap chdirs into a fresh temp dir, invokes runBootstrap, and
// returns the project root under which all generated files live.
func e2eBootstrap(t *testing.T, in Inputs) (target string, summary Summary) {
	t.Helper()
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
	}
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	sum, err := runBootstrap(context.Background(), e2eDeps(), in)
	require.NoError(t, err, "runBootstrap must succeed")
	target = filepath.Join(tmpDir, in.Name)
	return target, sum
}

// -----------------------------------------------------------------------------
// Bootstrap: default flags wire workflows + both hooks, no bus workflows.
// -----------------------------------------------------------------------------

// TestE2E_Bootstrap_DefaultFlags_WiresAllNonBusGenerators bootstraps a
// fresh project with default flags and asserts every non-bus generator
// landed coherently on disk under one shared .kit/generated.json
// manifest. Contract: Sections 6, 8 (defaults).
func TestE2E_Bootstrap_DefaultFlags_WiresAllNonBusGenerators(t *testing.T) {
	target, summary := e2eBootstrap(t, e2eInputs("demo", false))

	// Workflow callers (T-0772). Default Runtime = ["go"] → release + test.
	assert.FileExists(t, filepath.Join(target, ".github", "workflows", "release-go-caller.yml"))
	assert.FileExists(t, filepath.Join(target, ".github", "workflows", "test-go-caller.yml"))

	// Before-PR hook (T-0773) — bash + ps1 companion.
	assert.FileExists(t, filepath.Join(target, ".githooks", "pre-pr"))
	assert.FileExists(t, filepath.Join(target, ".githooks", "pre-pr.ps1"))

	// After-PR-open hook (T-0774) — bash + ps1 companion.
	assert.FileExists(t, filepath.Join(target, ".githooks", "post-pr-open"))
	assert.FileExists(t, filepath.Join(target, ".githooks", "post-pr-open.ps1"))

	// Bus workflows (T-0776) MUST NOT exist when --with-bus-workflows is
	// false. Default is opt-in (Section 8).
	for _, f := range buswf.Files() {
		_, statErr := os.Stat(filepath.Join(target, ".github", "workflows", f.Name))
		assert.True(t, os.IsNotExist(statErr),
			"%s must NOT exist with default flags (bus workflows are opt-in)", f.Name)
	}
	assert.Empty(t, summary.BusWorkflows,
		"summary.BusWorkflows must be empty when --with-bus-workflows is off")

	// Hook executability (POSIX): bash files must carry 0o755; .ps1 0o644.
	if runtime.GOOS != "windows" {
		for _, rel := range []string{".githooks/pre-pr", ".githooks/post-pr-open"} {
			info, err := os.Stat(filepath.Join(target, rel))
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&0o111,
				"%s must be executable on POSIX (mode=%v)", rel, info.Mode())
		}
		for _, rel := range []string{".githooks/pre-pr.ps1", ".githooks/post-pr-open.ps1"} {
			info, err := os.Stat(filepath.Join(target, rel))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
				"%s must be mode 0644 (no exec bit; Windows runs by extension)", rel)
		}
	}
}

// TestE2E_Bootstrap_ManifestSchemaAndHashes asserts the cross-generator
// .kit/generated.json manifest carries version=1 / generated_by=kit-init
// and one entry per generated file with the live file's SHA-256.
// Contract: Section 6.
func TestE2E_Bootstrap_ManifestSchemaAndHashes(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	manifestPath := filepath.Join(target, ".kit", "generated.json")
	require.FileExists(t, manifestPath)

	// Decode the manifest as raw JSON so we validate the on-disk schema
	// shape (rather than indirecting through Manifest / GeneratedManifest
	// which would mask schema regressions like a typo in `generated_by`).
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var raw struct {
		Version     int `json:"version"`
		GeneratedBy string `json:"generated_by"`
		Files       []struct {
			Path        string `json:"path"`
			SHA256      string `json:"sha256"`
			GeneratedAt string `json:"generatedAt"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(data, &raw), "manifest must be well-formed JSON")
	assert.Equal(t, 1, raw.Version, "manifest version must be 1")
	assert.Equal(t, "kit-init", raw.GeneratedBy, "manifest generated_by must be \"kit-init\"")
	require.NotEmpty(t, raw.Files, "manifest must list at least one generated file")

	// Index the manifest by path so per-generator hash checks read clearly.
	byPath := make(map[string]string, len(raw.Files))
	for _, f := range raw.Files {
		assert.NotEmpty(t, f.SHA256, "manifest entry %q must carry sha256", f.Path)
		assert.NotEmpty(t, f.GeneratedAt, "manifest entry %q must carry generatedAt", f.Path)
		// generatedAt is RFC3339 UTC; require trailing "Z" so a future
		// regression to a local-time format surfaces.
		assert.Contains(t, f.GeneratedAt, "Z",
			"manifest entry %q generatedAt must be RFC3339 UTC (got %q)", f.Path, f.GeneratedAt)
		byPath[f.Path] = f.SHA256
	}

	// Each generator's known paths must be present with a hash that
	// matches the live file's content. We assert against the bash hook
	// + ps1 (T-0773) and the workflow callers (T-0772). post-pr-open
	// (T-0774) does not currently land in this manifest (Summary surfaces
	// it via PostHookResult); see "real bugs found" report.
	mustMatch := []string{
		PrePrHookPath,
		PrePrHookPs1Path,
		".github/workflows/release-go-caller.yml",
		".github/workflows/test-go-caller.yml",
	}
	for _, rel := range mustMatch {
		t.Run(rel, func(t *testing.T) {
			want, ok := byPath[rel]
			require.True(t, ok, "manifest must carry entry for %s", rel)
			live, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
			require.NoError(t, err)
			assert.Equal(t, sha256Of(live), want,
				"manifest hash for %s must match live file SHA-256", rel)
		})
	}
}

// TestE2E_Bootstrap_WithBusWorkflows_AllFourLand bootstraps a fresh
// project with --with-bus-workflows and asserts the four kit-bus-*.yml
// files land alongside the workflow callers and hooks. Contract:
// Sections 3, 6, 8.
func TestE2E_Bootstrap_WithBusWorkflows_AllFourLand(t *testing.T) {
	target, summary := e2eBootstrap(t, e2eInputs("demo", true))

	// All four bus workflows on disk.
	for _, f := range buswf.Files() {
		assert.FileExists(t, filepath.Join(target, ".github", "workflows", f.Name),
			"bus workflow %s must land with --with-bus-workflows", f.Name)
	}

	// Workflow callers + hooks still land in lockstep.
	assert.FileExists(t, filepath.Join(target, ".github", "workflows", "release-go-caller.yml"))
	assert.FileExists(t, filepath.Join(target, ".githooks", "pre-pr"))
	assert.FileExists(t, filepath.Join(target, ".githooks", "post-pr-open"))

	// Summary surfaces all four bus plan entries.
	assert.Len(t, summary.BusWorkflows, 4)
	for _, e := range summary.BusWorkflows {
		assert.Equal(t, buswf.ActionWrite, e.Action)
		assert.Equal(t, buswf.ReasonNew, e.Reason)
	}

	// Manifest tracks all four bus files (verified via buswf.ReadManifest
	// which decodes the same on-disk file the workflow generator writes).
	m, err := buswf.ReadManifest(target)
	require.NoError(t, err)
	byPath := make(map[string]buswf.ManifestFile, len(m.Files))
	for _, f := range m.Files {
		byPath[f.Path] = f
	}
	for _, f := range buswf.Files() {
		rel := ".github/workflows/" + f.Name
		entry, ok := byPath[rel]
		require.True(t, ok, "bus workflow %s missing from manifest", rel)
		assert.Equal(t, buswf.SHA256(f.Body), entry.SHA256,
			"manifest hash for %s must match generator output", rel)
	}
}

// -----------------------------------------------------------------------------
// Augment: hash-match → refresh-in-place; user-edited → suggest sibling;
// suggestion cleanup → reclaim. Contract Section 6.
// -----------------------------------------------------------------------------

// TestE2E_Augment_HashMatch_RefreshesAndKeepsAllGenerators seeds a project
// directory with a manifest whose entries match the on-disk files
// byte-for-byte across every generator, then re-runs the generators and
// asserts that nothing diverges and no stray .kit-suggested files appear.
//
// The integration value: each generator has its own augment policy; this
// confirms they compose without overwriting each other's manifest entries
// or producing spurious suggestions.
func TestE2E_Augment_HashMatch_RefreshesAndKeepsAllGenerators(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", true))

	// Snapshot manifest after first run to confirm a second run leaves
	// the path set intact (paths in, paths out).
	manifestPath := filepath.Join(target, ".kit", "generated.json")
	before, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	// Re-run the workflow + bus generators in augment mode. We invoke them
	// directly (rather than via runAugment, which expects a separate cwd)
	// because runAugment scaffolds the cli-go template a second time into
	// the same directory — that would noise up the on-disk surface we're
	// asserting against. The generators are the unit of contract for
	// Section 6's refresh logic, so direct invocation is the cleanest seam.
	in := e2eInputs("demo", true)
	in.WithBusWorkflows = true

	wfActions, err := renderWorkflows(target, in.Runtime, in, nil)
	require.NoError(t, err)
	for _, a := range wfActions {
		// Identical on-disk content → manifest entry already matches →
		// skip-unchanged is the right action.
		assert.Equal(t, "skip-unchanged", a.Action,
			"workflow %s should report skip-unchanged on identical re-run", a.Path)
		assert.Empty(t, a.SuggestedPath,
			"workflow %s must NOT produce a sibling when content matches", a.Path)
	}

	preprResult, err := GeneratePrePrHook(target, false, fixedTime())
	require.NoError(t, err)
	// For the hook files specifically (bash + ps1) the content is
	// byte-identical, so skip-unchanged is the right action. The
	// .kit/generated.json row is excluded: the manifest accumulates
	// entries from sibling generators (workflows, bus, …) so the bytes
	// prepr would write differ from what's on disk by design.
	// ManifestUpdate is the expected action for the manifest row.
	for _, r := range preprResult.Files {
		switch r.Path {
		case PrePrHookPath, PrePrHookPs1Path:
			assert.Equal(t, ActionSkipUnchanged, r.Action,
				"pre-pr %s should report skip-unchanged on identical re-run", r.Path)
		case GeneratedManifestPath:
			// Shared manifest is co-owned by every generator; prepr's
			// view of the would-be bytes differs from on-disk whenever
			// any other generator has appended an entry. The hook
			// content itself is unchanged, which is the load-bearing
			// assertion above.
			assert.Contains(t,
				[]PrePrAction{ActionSkipUnchanged, ActionManifestUpdate},
				r.Action,
				"manifest row should be skip-unchanged or manifest-update; got %s", r.Action)
		}
	}

	postRes, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkipUnchanged, postRes.Action,
		"post-pr-open should report skip-unchanged on identical re-run")

	busPlan, err := buswf.WriteAll(buswf.Defaults(target))
	require.NoError(t, err)
	for _, e := range busPlan.Entries {
		assert.Equal(t, buswf.ActionSkipUnchanged, e.Action,
			"bus workflow %s should report skip-unchanged on identical re-run", e.Path)
	}

	// No .kit-suggested sibling should have appeared anywhere under target.
	assertNoSuggestedSiblings(t, target)

	// The manifest's path set is unchanged; only generatedAt timestamps
	// may differ. Compare the sorted path lists.
	after, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, manifestPaths(t, before), manifestPaths(t, after),
		"manifest path set must remain stable on hash-match refresh")
}

// TestE2E_Augment_UserEdited_WritesSuggestSibling seeds a kit-managed
// workflow caller, then mutates it on disk so its hash diverges from
// the manifest. The next augment run must NOT overwrite the user-edited
// file; instead it writes a sibling at <path>.kit-suggested with the
// kit-canonical content. Contract Section 6.
func TestE2E_Augment_UserEdited_WritesSuggestSibling(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	relPath := ".github/workflows/release-go-caller.yml"
	absPath := filepath.Join(target, filepath.FromSlash(relPath))

	// Snapshot kit's would-be content so we can verify the sibling
	// carries it verbatim. Read first (matches the live + manifest), then
	// mutate the live file with a user-edit marker.
	kitBytes, err := os.ReadFile(absPath)
	require.NoError(t, err)
	userEdit := append([]byte("# USER EDIT: keep my changes!\n"), kitBytes...)
	require.NoError(t, os.WriteFile(absPath, userEdit, 0o644))

	in := e2eInputs("demo", false)
	actions, err := renderWorkflows(target, in.Runtime, in, nil)
	require.NoError(t, err)

	// Locate the action for the user-edited path.
	var action WorkflowAction
	for _, a := range actions {
		if a.Path == relPath {
			action = a
			break
		}
	}
	require.Equal(t, relPath, action.Path, "no action recorded for %s", relPath)
	assert.Equal(t, "suggest-sibling", action.Action,
		"user-edited file must route to suggest-sibling")
	assert.Equal(t, "user-edited", action.Reason)
	assert.Equal(t, relPath+".kit-suggested", action.SuggestedPath,
		"suggested_path must be the sibling")

	// Live file untouched.
	got, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.Equal(t, string(userEdit), string(got),
		"user-edited file must be preserved byte-for-byte")

	// Sibling materialised with kit's canonical content.
	sibling, err := os.ReadFile(absPath + ".kit-suggested")
	require.NoError(t, err)
	assert.Equal(t, string(kitBytes), string(sibling),
		"<path>.kit-suggested must carry kit's would-be content")

	// Manifest entry for the path is NOT updated to the user's hash —
	// kit doesn't claim user-edited files. We verify by reading the
	// manifest and confirming the recorded SHA still matches kit's
	// canonical content (the manifest hash + the live hash diverge,
	// which is exactly the "user-edited" state).
	m, err := buswf.ReadManifest(target)
	require.NoError(t, err)
	var found bool
	for _, f := range m.Files {
		if f.Path == relPath {
			found = true
			assert.Equal(t, sha256Of(kitBytes), f.SHA256,
				"manifest hash for user-edited path must stay at kit's content")
		}
	}
	require.True(t, found, "manifest must still track %s after user-edit augment run", relPath)
}

// TestE2E_Augment_SuggestionCleanup_RemovesAcceptedSibling pins the
// Section 6 convergence rule: when the live file's content equals an
// existing .kit-suggested sibling (user accepted the suggestion), the
// sibling must be removed on the next augment run.
func TestE2E_Augment_SuggestionCleanup_RemovesAcceptedSibling(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	relPath := ".github/workflows/release-go-caller.yml"
	absPath := filepath.Join(target, filepath.FromSlash(relPath))

	// Seed a stale .kit-suggested sibling whose content equals the live
	// file. (In a real workflow the user would have copied the sibling
	// over the live file; we shortcut that step.)
	kitBytes, err := os.ReadFile(absPath)
	require.NoError(t, err)
	suggested := absPath + ".kit-suggested"
	require.NoError(t, os.WriteFile(suggested, kitBytes, 0o644))

	in := e2eInputs("demo", false)
	_, err = renderWorkflows(target, in.Runtime, in, nil)
	require.NoError(t, err)

	// Sibling must be gone — accepted suggestion was reclaimed.
	_, statErr := os.Stat(suggested)
	assert.True(t, os.IsNotExist(statErr),
		"byte-identical .kit-suggested sibling must be removed on next run; statErr=%v", statErr)
}

// TestE2E_Augment_PrePrHook_UserEdited_KeepsOriginal mirrors the
// workflow-caller user-edit case for the before-PR hook. The pre-pr
// generator's augment policy is implemented separately from the
// workflow caller's; this test asserts both generators apply Section 6
// consistently. Contract Section 6.
func TestE2E_Augment_PrePrHook_UserEdited_KeepsOriginal(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	hookPath := filepath.Join(target, PrePrHookPath)
	customBody := []byte("#!/usr/bin/env bash\n# user-customized hook\nexit 0\n")
	require.NoError(t, os.WriteFile(hookPath, customBody, 0o755))

	res, err := GeneratePrePrHook(target, false, fixedTime())
	require.NoError(t, err)

	var hookRow PrePrFileReport
	for _, r := range res.Files {
		if r.Path == PrePrHookPath {
			hookRow = r
		}
	}
	assert.Equal(t, ActionSuggestSibling, hookRow.Action)
	assert.Equal(t, ReasonUserEdited, hookRow.Reason)

	// Live file untouched, sibling exists with canonical hook bytes.
	got, err := os.ReadFile(hookPath)
	require.NoError(t, err)
	assert.Equal(t, string(customBody), string(got))

	canonical, err := loadPrePrHookBytes()
	require.NoError(t, err, "loadPrePrHookBytes must succeed (embedded asset)")
	sibling, err := os.ReadFile(hookPath + ".kit-suggested")
	require.NoError(t, err)
	assert.Equal(t, string(canonical), string(sibling))
}

// -----------------------------------------------------------------------------
// Dry-run: no on-disk side effects across any generator. Contract Section 6.
// -----------------------------------------------------------------------------

// TestE2E_DryRun_AllGenerators_NoFileSystemChanges runs a fresh bootstrap
// with dry-run set on every generator's path simultaneously and asserts
// not a single file is written. The Summary still reports the planned
// actions so adopters get a complete preview.
func TestE2E_DryRun_AllGenerators_NoFileSystemChanges(t *testing.T) {
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
	}
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	in := e2eInputs("demo", true)
	in.DryRun = true

	summary, err := runBootstrap(context.Background(), e2eDeps(), in)
	require.NoError(t, err)

	target := filepath.Join(tmpDir, "demo")

	// Workflow callers, bus workflows, and hook scripts: zero files on disk.
	for _, rel := range []string{
		".github/workflows/release-go-caller.yml",
		".github/workflows/test-go-caller.yml",
		".githooks/pre-pr",
		".githooks/pre-pr.ps1",
		".githooks/post-pr-open",
		".githooks/post-pr-open.ps1",
		".kit/generated.json",
	} {
		_, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(rel)))
		assert.True(t, os.IsNotExist(statErr),
			"dry-run wrote %s; expected no on-disk side effect", rel)
	}
	for _, f := range buswf.Files() {
		_, statErr := os.Stat(filepath.Join(target, ".github", "workflows", f.Name))
		assert.True(t, os.IsNotExist(statErr),
			"dry-run wrote bus workflow %s", f.Name)
	}

	// Summary still reports the would-be plan for every generator.
	assert.NotEmpty(t, summary.Workflows, "dry-run summary must report workflow plan")
	assert.NotNil(t, summary.PrePrHook, "dry-run summary must report pre-pr plan")
	assert.NotEmpty(t, summary.PrePrHook.Files, "dry-run summary must include pre-pr file rows")
	assert.Len(t, summary.BusWorkflows, 4, "dry-run summary must report all four bus plan entries")
}

// -----------------------------------------------------------------------------
// Bus event workflows: fixtures-style YAML + topic validation. Contract
// Sections 2, 3. Per-file structural checks live in buswf/files_test.go;
// this test asserts the cross-cutting invariants every generated file must
// satisfy in the same loop so a regression in one file fails the test
// with the same shape as a regression in any other.
// -----------------------------------------------------------------------------

// TestE2E_BusWorkflows_TopicAndOnTriggerAndPayloadFields walks the four
// generated workflow bodies and checks, for each:
//   - the emitted topic is a valid bus topic (passes bus.ValidateTopic),
//   - the on: trigger matches the contract's expected shape per topic,
//   - the job-level if: gate references both KIT_BUS_ENABLED and
//     KIT_BUS_INGRESS_URL (per Section 3),
//   - the env block forwards all four delivery env vars,
//   - the body references the per-topic payload fields the spec calls out
//     (Section 2: html_url, head_sha, base_sha, etc).
func TestE2E_BusWorkflows_TopicAndOnTriggerAndPayloadFields(t *testing.T) {
	type wantTrigger struct {
		root  string   // e.g. "workflow_run" or "pull_request"
		types []string // e.g. ["completed"], ["created"], ["closed"]
	}
	cases := map[string]struct {
		trigger        wantTrigger
		ifGuardSubstrs []string // additional event-shape guards in the job if:
		// payloadRefs: substrings the workflow body must mention so the
		// downstream payload carries the required fields.
		payloadRefs []string
	}{
		"github.pr.run.completed": {
			trigger: wantTrigger{root: "workflow_run", types: []string{"completed"}},
			ifGuardSubstrs: []string{
				"github.event.workflow_run.event == 'pull_request'",
			},
			payloadRefs: []string{
				"github.event.workflow_run.html_url",
				"github.event.workflow_run.head_sha",
				"github.event.workflow_run.pull_requests[0].base.sha",
			},
		},
		"github.pr.comment.created": {
			trigger: wantTrigger{root: "pull_request_review_comment", types: []string{"created"}},
			payloadRefs: []string{
				"github.event.comment.html_url",
				"github.event.pull_request.head.sha",
				"github.event.pull_request.base.sha",
			},
		},
		"github.pr.pull.merged": {
			trigger: wantTrigger{root: "pull_request", types: []string{"closed"}},
			ifGuardSubstrs: []string{
				"github.event.pull_request.merged == true",
			},
			payloadRefs: []string{
				"github.event.pull_request.merge_commit_sha",
				"github.event.pull_request.html_url",
				"github.event.pull_request.head.sha",
			},
		},
		"github.pr.pull.closed": {
			trigger: wantTrigger{root: "pull_request", types: []string{"closed"}},
			ifGuardSubstrs: []string{
				"github.event.pull_request.merged == false",
			},
			payloadRefs: []string{
				"github.event.pull_request.closed_at",
				"github.event.pull_request.html_url",
				"github.event.pull_request.head.sha",
			},
		},
	}

	for _, file := range buswf.Files() {
		file := file
		t.Run(string(file.Topic), func(t *testing.T) {
			// Topic must pass bus.ValidateTopic (Section 2).
			require.NoError(t, bus.ValidateTopic(bus.Topic(file.Topic)),
				"topic %q must pass bus.ValidateTopic", file.Topic)

			body := string(file.Body)
			c, ok := cases[file.Topic]
			require.True(t, ok, "no expectation registered for topic %s", file.Topic)

			// on: trigger root + types check via YAML parse so a future
			// rendering tweak (extra whitespace, alias quoting) keeps the
			// test green as long as the structural shape stays right.
			var doc map[string]any
			require.NoError(t, yaml.Unmarshal(file.Body, &doc),
				"workflow body must parse as YAML")
			on, ok := doc["on"].(map[string]any)
			require.True(t, ok, "on: must be a mapping; got %T", doc["on"])
			trigger, ok := on[c.trigger.root].(map[string]any)
			require.True(t, ok, "on: must declare %s: trigger (got %v)", c.trigger.root, on)
			types, ok := trigger["types"].([]any)
			require.True(t, ok, "on.%s.types must be a list", c.trigger.root)
			gotTypes := make([]string, 0, len(types))
			for i, ty := range types {
				s, ok := ty.(string)
				require.True(t, ok, "on.%s.types[%d] must be a string; got %T (%v)", c.trigger.root, i, ty, ty)
				gotTypes = append(gotTypes, s)
			}
			assert.ElementsMatch(t, c.trigger.types, gotTypes,
				"on.%s.types must equal %v", c.trigger.root, c.trigger.types)

			// Job-level if: must reference both gating conditions
			// (Section 3). String-scan keeps the assertion legible.
			assert.Contains(t, body, "vars.KIT_BUS_ENABLED == 'true'",
				"emit-bus if: must check KIT_BUS_ENABLED")
			assert.Contains(t, body, "vars.KIT_BUS_INGRESS_URL != ''",
				"emit-bus if: must check KIT_BUS_INGRESS_URL")
			for _, g := range c.ifGuardSubstrs {
				assert.Contains(t, body, g,
					"emit-bus if: must include event-shape guard %q", g)
			}

			// Env block forwards all four delivery env vars (Section 3).
			required := []string{
				"KIT_BUS_INGRESS_URL: ${{ vars.KIT_BUS_INGRESS_URL }}",
				"KIT_BUS_TOKEN: ${{ secrets.KIT_BUS_TOKEN }}",
				"KIT_BUS_SIGNING_KEY: ${{ secrets.KIT_BUS_SIGNING_KEY }}",
				"KIT_BUS_STRICT: ${{ vars.KIT_BUS_STRICT }}",
			}
			for _, want := range required {
				assert.Contains(t, body, want,
					"workflow %s must forward %s to the helper", file.Name, want)
			}

			// Per-topic payload field references (Section 2).
			for _, ref := range c.payloadRefs {
				assert.Contains(t, body, ref,
					"workflow %s must reference payload field %s", file.Name, ref)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Post-pr-open hook: cross-generator integration via bootstrap. Bash-level
// semantics (push/pull path, event family → topic mapping, dedup,
// fail-open) live in posthook_test.go; this file only verifies the hook
// content surfaced by runBootstrap matches the canonical embedded asset
// (i.e. the bootstrap path doesn't mutate the hook on the way out).
// -----------------------------------------------------------------------------

// TestE2E_Bootstrap_PostPROpenHook_ContentMatchesAsset asserts that the
// hook actually written by runBootstrap is byte-identical to
// PostPROpenHookContent() — i.e. there is no in-flight token substitution
// or template render that would silently drift the on-disk hook away from
// the contract-pinned content. Contract Section 5 (the hook behaviour
// described there is implemented by the embedded asset).
func TestE2E_Bootstrap_PostPROpenHook_ContentMatchesAsset(t *testing.T) {
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	got, err := os.ReadFile(filepath.Join(target, ".githooks", "post-pr-open"))
	require.NoError(t, err)
	want := PostPROpenHookContent()
	assert.Equal(t, string(want), string(got),
		"post-pr-open hook on disk must match the embedded canonical asset")

	gotPs1, err := os.ReadFile(filepath.Join(target, ".githooks", "post-pr-open.ps1"))
	require.NoError(t, err)
	wantPs1 := PostPROpenPS1Content()
	assert.Equal(t, string(wantPs1), string(gotPs1),
		"post-pr-open.ps1 on disk must match the embedded canonical asset")
}

// TestE2E_PostPROpenHook_PushPath_LiveBusServer_NoLocalTask exercises the
// full push-path semantics (Section 5) end-to-end: real httptest server
// returns 200 on /healthz within the 5s window; the generated hook
// MUST exit 0 without invoking tlc. The integration value over the
// existing per-hook tests is that we render the hook via the bootstrap
// flow (rather than the harness's direct GeneratePostPROpenHook), so any
// regression in bootstrap's hook-write path surfaces here.
func TestE2E_PostPROpenHook_PushPath_LiveBusServer_NoLocalTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash hook integration requires /bin/bash; see posthook_ps1_test.go for the Windows surface")
	}
	target, _ := e2eBootstrap(t, e2eInputs("demo", false))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newE2EHookHarness(t, target)
	h.stubGH(t, `{"number":777,"url":"https://github.com/hop-top/example/pull/777","headRefName":"t-0775-e2e-coverage","headRefOid":"abc1234567890","title":"e2e coverage","body":"Implements T-0775","baseRepository":{"name":"example","owner":{"login":"hop-top"}}}`)
	h.stubTLC(t)
	h.stubCurlAgainst(t, srv.URL+"/healthz")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": srv.URL,
	})
	assert.Equal(t, 0, exit, "push path must exit 0")
	assert.Contains(t, stderr, "bus ingress healthy",
		"push path must log that the bus host is taking the follow-up")
	assert.NoFileExists(t, h.tlcLog,
		"push path with healthy bus must NOT invoke tlc")
}

// -----------------------------------------------------------------------------
// kit-bus-emit helper: cross-binary integration. The unit tests in
// cmd/kit-bus-emit/post_test.go + main_test.go already cover bearer/
// signing-key precedence + fail-open/fail-closed via the in-process run()
// function. We don't duplicate them here; this section's value-add is the
// e2e wire-up: the workflow generator + helper binary together honor the
// strict-mode env var pinned in Section 3.
//
// The actual kit-bus-emit binary is exec-driven (go install …@latest), so
// invoking it through a workflow at test time would require a full GH
// Actions runtime. The contract-pinned behavior is verified by:
//   - the bus workflow body forwards KIT_BUS_STRICT (TestE2E_BusWorkflows_…),
//   - the helper binary's run() respects KIT_BUS_STRICT (main_test.go).
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Helpers.
// -----------------------------------------------------------------------------

// assertNoSuggestedSiblings walks target and fails the test if it finds
// any .kit-suggested file. Used by augment hash-match cases where the
// expectation is "nothing should diverge".
func assertNoSuggestedSiblings(t *testing.T, target string) {
	t.Helper()
	var siblings []string
	err := filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".kit-suggested") {
			siblings = append(siblings, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, siblings,
		"hash-match augment must not produce .kit-suggested siblings; found: %v", siblings)
}

// manifestPaths returns the sorted list of `path` entries in the given
// .kit/generated.json byte body. Used to assert manifest topology
// (which paths are tracked) without coupling to generatedAt timestamps.
func manifestPaths(t *testing.T, data []byte) []string {
	t.Helper()
	var raw struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	paths := make([]string, 0, len(raw.Files))
	for _, f := range raw.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	return paths
}

// e2eHookHarness mirrors the per-hook harness in posthook_test.go but
// drives the hook that runBootstrap actually wrote (rather than calling
// GeneratePostPROpenHook directly). Keeps the e2e surface honest about
// the bootstrap → hook handoff.
type e2eHookHarness struct {
	hookPath string
	pathDir  string
	tlcLog   string
	curlLog  string
}

func newE2EHookHarness(t *testing.T, projectRoot string) *e2eHookHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("e2e hook harness requires /bin/bash")
	}
	return &e2eHookHarness{
		hookPath: filepath.Join(projectRoot, ".githooks", "post-pr-open"),
		pathDir:  t.TempDir(),
		tlcLog:   filepath.Join(t.TempDir(), "tlc.log"),
		curlLog:  filepath.Join(t.TempDir(), "curl.log"),
	}
}

// stubGH writes a gh stub identical in shape to the one in posthook_test.go.
// We intentionally re-author it here (rather than sharing through an
// exported helper) because this e2e file is in the same package, and
// importing the unit-test harness via a method on its receiver would
// couple two test files that should stay independently editable.
func (h *e2eHookHarness) stubGH(t *testing.T, json string) {
	t.Helper()
	jqPath, err := lookPath("jq")
	if err != nil {
		t.Skip("e2e gh stub requires jq for --jq passthrough")
	}
	script := `#!/usr/bin/env bash
set -u
JQ='` + jqPath + `'
JSON=$(cat <<'GHJSON'
` + json + `
GHJSON
)
JQ_EXPR=""
saw_jq=0
for arg in "$@"; do
  if [ "${saw_jq}" = "1" ]; then
    JQ_EXPR="${arg}"
    saw_jq=0
    continue
  fi
  case "${arg}" in
    --jq) saw_jq=1 ;;
  esac
done
case "$1 $2" in
  "pr view")
    if [ -n "${JQ_EXPR}" ]; then
      printf '%s' "${JSON}" | "${JQ}" -r "${JQ_EXPR}"
    else
      printf '%s\n' "${JSON}"
    fi
    ;;
  "repo view")
    if [ -n "${JQ_EXPR}" ]; then
      printf '%s' "${REPO_NAME_WITH_OWNER:-}" | "${JQ}" -R -r "${JQ_EXPR} // \"\""
    else
      printf '{"nameWithOwner":"%s"}\n' "${REPO_NAME_WITH_OWNER:-}"
    fi
    ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "gh"), []byte(script), 0o755))
}

func (h *e2eHookHarness) stubTLC(t *testing.T) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -u
echo "$@" >> "` + h.tlcLog + `"
case "$1" in
  task)
    case "$2" in
      list)  printf '%s' "${TLC_LIST_OUTPUT:-[]}" ;;
      create) exit 0 ;;
    esac
    ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "tlc"), []byte(script), 0o755))
}

func (h *e2eHookHarness) stubCurlAgainst(t *testing.T, httpTestURL string) {
	t.Helper()
	realCurl, err := lookPath("curl")
	if err != nil {
		t.Skip("real curl not on PATH; skipping HTTP-backed probe test")
	}
	script := `#!/usr/bin/env bash
ARGS=("$@")
HOOK_URL="${ARGS[$((${#ARGS[@]}-1))]}"
printf '%s\n' "${HOOK_URL}" >> "` + h.curlLog + `"
URL="` + httpTestURL + `"
exec ` + realCurl + ` "${ARGS[@]:0:$((${#ARGS[@]}-1))}" "${URL}"
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "curl"), []byte(script), 0o755))
}

func (h *e2eHookHarness) run(t *testing.T, env map[string]string) (stderr string, exitCode int) {
	t.Helper()
	envPath := h.pathDir + ":/usr/bin:/bin"
	cmd := exec.Command("/bin/bash", h.hookPath)
	cmd.Env = []string{"PATH=" + envPath}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(interface{ ExitCode() int }); ok {
		exitCode = exitErr.ExitCode()
	}
	return string(out), exitCode
}

// lookPath is a tiny indirection around exec.LookPath so the e2e harness
// stays decoupled from the unit-test harness's name.
func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}
