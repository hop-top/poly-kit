// Tests for how the release-please generator treats a repo that already
// runs release-please from a hand-written workflow: never a second live
// release-please workflow, never a lost custom job, never a replaced
// file whose content is not recoverable from git.
package kitinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shapes observed across hop-top adopters.
const (
	// cxr-style: App token, org config paths.
	legacyAppToken = `name: release-please

on:
  push:
    branches: [main]
  workflow_dispatch: {}

permissions:
  contents: write
  pull-requests: write

jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      # Mint a release-bot token.
      - uses: actions/create-github-app-token@v1
        id: app-token
        with:
          app-id: ${{ secrets.RELEASE_BOT_APP_ID }}
          private-key: ${{ secrets.RELEASE_BOT_PRIVATE_KEY }}

      - uses: googleapis/release-please-action@v4
        with:
          config-file: .github/release-please-config.json
          manifest-file: .github/.release-please-manifest.json
          token: ${{ steps.app-token.outputs.token }}
`
	// poly-kit-style: two branches, checkout, target-branch from ref, own guard.
	legacyMultiBranch = `name: release-please
on:
  push:
    branches: [main, next]
  workflow_dispatch: {}
concurrency:
  group: release-please-${{ github.ref }}
  cancel-in-progress: true
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: actions/create-github-app-token@v3
        id: app-token
        with:
          app-id: ${{ secrets.RELEASE_BOT_APP_ID }}
          private-key: ${{ secrets.RELEASE_BOT_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v5
        with:
          config-file: .github/release-please-config.json
          manifest-file: .github/.release-please-manifest.json
          target-branch: ${{ github.ref_name }}
          token: ${{ steps.app-token.outputs.token }}
`
	// rlz-style (release.yml): default GITHUB_TOKEN, root config paths.
	legacyRootConfig = `name: release-please
on:
  push:
    branches: [main]
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
`
	// spec-12fc-style: PAT.
	legacyPAT = `name: release-please
on:
  push:
    branches: [main]
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          config-file: .github/release-please-config.json
          manifest-file: .github/.release-please-manifest.json
          token: ${{ secrets.GH_RELEASE_PLEASE_PAT }}
`
	// foo-style: manual only.
	legacyDispatchOnly = `name: release-please
on:
  workflow_dispatch: {}
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/create-github-app-token@v3
        id: app-token
        with:
          app-id: ${{ secrets.RELEASE_BOT_APP_ID }}
          private-key: ${{ secrets.RELEASE_BOT_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v5
        with:
          config-file: .github/release-please-config.json
          manifest-file: .github/.release-please-manifest.json
          token: ${{ steps.app-token.outputs.token }}
`
	// inv-style: publish job chained on release outputs.
	legacyWithPublish = `name: Release Please
on:
  push:
    branches: [main]
permissions:
  contents: write
  pull-requests: write
jobs:
  release-please:
    runs-on: ubuntu-latest
    outputs:
      rs_release_created: ${{ steps.release.outputs.release_created }}
    steps:
      - uses: googleapis/release-please-action@v4
        id: release
  release-rs:
    needs: release-please
    if: ${{ needs.release-please.outputs.rs_release_created == 'true' }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: cargo publish --token ${{ secrets.CARGO_REGISTRY_TOKEN }}
`
)

func migrateInputs() Inputs {
	in := rpInputs()
	in.MigrateReleasePlease = true
	return in
}

func callerSettings(t *testing.T, content string) releasePleaseSettings {
	t.Helper()
	wf, isRP := classifyReleasePleaseWorkflow(rpCaller, []byte(content))
	require.True(t, isRP, content)
	require.True(t, wf.Plain, "%s\n%s", wf.Why, content)
	require.True(t, wf.KitCaller, "live file must be the kit caller:\n%s", content)
	return wf.Settings
}

// --- without --migrate-release-please: report, never a second live run -------

func TestRP_LegacyAtCallerPath_NoFlag_SuggestsAndKeeps(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyAppToken})
	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)

	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "suggest-sibling", a.Action)
	assert.Equal(t, "existing-release-please", a.Reason)
	assert.Contains(t, a.Detail, "--migrate-release-please")
	assert.Equal(t, legacyAppToken, readRel(t, target, rpCaller), "original untouched")
	assert.Equal(t,
		renderReleasePleaseCaller(releasePleaseSettings{Branches: []string{"main"}, Dispatch: true}),
		readRel(t, target, rpCaller+suggestedSuffix))
	assertAbsent(t, target, manifestRelPath)
}

func TestRP_LegacyElsewhere_NoFlag_NoSecondLiveWorkflow(t *testing.T) {
	legacy := ".github/workflows/release.yml"
	target := gitRepoWith(t, map[string]string{legacy: legacyRootConfig})
	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)

	assertAbsent(t, target, rpCaller)
	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "suggest-sibling", a.Action)
	assert.Equal(t, "existing-release-please", a.Reason)
	assert.Contains(t, a.Detail, legacy)
	// Suggestion carries the legacy settings (root config paths).
	assert.Equal(t, releasePleaseSettings{
		Branches:     []string{"main"},
		ConfigFile:   "release-please-config.json",
		ManifestFile: ".release-please-manifest.json",
	}, callerSettingsLoose(t, readRel(t, target, rpCaller+suggestedSuffix)))

	k := mustAction(t, actions, legacy)
	assert.Equal(t, "keep", k.Action)
	assert.Equal(t, legacyRootConfig, readRel(t, target, legacy))
}

func TestRP_Rerun_NoFlag_IsStable(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyAppToken})
	_, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	sibling := readRel(t, target, rpCaller+suggestedSuffix)

	_, err = renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, legacyAppToken, readRel(t, target, rpCaller))
	assert.Equal(t, sibling, readRel(t, target, rpCaller+suggestedSuffix))
	assertAbsent(t, target, manifestRelPath)
}

// --- --migrate-release-please on a plain, committed workflow -----------------

func TestRP_Migrate_InPlace_CarriesSettings(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyMultiBranch})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)

	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "write", a.Action)
	assert.Equal(t, "migrated", a.Reason)
	live := readRel(t, target, rpCaller)
	assert.Equal(t, releasePleaseSettings{Branches: []string{"main", "next"}, Dispatch: true},
		callerSettings(t, live), "ref_name target and org default paths collapse to defaults")
	assertAbsent(t, target, rpCaller+suggestedSuffix)

	m := readManifestFile(t, target)
	require.Len(t, m.Files, 1)
	assert.Equal(t, sha256Hex([]byte(live)), m.Files[0].SHA256)
}

func TestRP_Migrate_OtherPath_WritesCallerRemovesLegacy_ThenNoOp(t *testing.T) {
	legacy := ".github/workflows/release.yml"
	target := gitRepoWith(t, map[string]string{
		legacy:                          legacyRootConfig,
		"release-please-config.json":    `{"packages":{".":{}}}`,
		".release-please-manifest.json": `{}`,
	})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)

	assert.Equal(t, "migrated", mustAction(t, actions, rpCaller).Reason)
	r := mustAction(t, actions, legacy)
	assert.Equal(t, "remove", r.Action)
	assert.Equal(t, "migrated", r.Reason)
	assertAbsent(t, target, legacy)
	live := readRel(t, target, rpCaller)
	assert.Equal(t, releasePleaseSettings{
		Branches:     []string{"main"},
		ConfigFile:   "release-please-config.json",
		ManifestFile: ".release-please-manifest.json",
	}, callerSettings(t, live), "push-only trigger kept (no dispatch added)")
	// Token change is spelled out.
	assert.Contains(t, mustAction(t, actions, rpCaller).Detail, "GITHUB_TOKEN")

	// Second run (flag still on): nothing left to do.
	actions, err = renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	require.Len(t, actions, 1, "%+v", actions)
	assert.Equal(t, "skip-unchanged", actions[0].Action)
	assert.Equal(t, live, readRel(t, target, rpCaller))
}

func TestRP_Migrate_PAT_ReportsTokenSwitch(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyPAT})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "migrated", a.Reason)
	assert.Contains(t, a.Detail, "secrets.GH_RELEASE_PLEASE_PAT")
	assert.Contains(t, a.Detail, "release-bot")
}

func TestRP_Migrate_DispatchOnly_StaysManual(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyDispatchOnly})
	_, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, releasePleaseSettings{Dispatch: true}, callerSettings(t, readRel(t, target, rpCaller)))
}

func TestRP_Migrate_DryRun_TouchesNothing(t *testing.T) {
	legacy := ".github/workflows/release.yml"
	target := gitRepoWith(t, map[string]string{legacy: legacyRootConfig})
	in := migrateInputs()
	in.DryRun = true
	actions, err := renderReleasePlease(target, in, fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "remove", mustAction(t, actions, legacy).Action)
	assert.Equal(t, legacyRootConfig, readRel(t, target, legacy))
	assertAbsent(t, target, rpCaller)
	assertAbsent(t, target, manifestRelPath)
}

// --- --migrate-release-please refuses what it cannot reproduce ----------------

func TestRP_Migrate_CustomJobs_NeverTouched(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyWithPublish})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)

	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "suggest-sibling", a.Action)
	assert.Equal(t, "custom-release-please", a.Reason)
	assert.Contains(t, a.Detail, "release-rs")
	assert.Equal(t, legacyWithPublish, readRel(t, target, rpCaller))
	assertAbsent(t, target, manifestRelPath)
}

func TestRP_Migrate_CustomElsewhere_NoSecondLiveWorkflow(t *testing.T) {
	legacy := ".github/workflows/release.yml"
	target := gitRepoWith(t, map[string]string{legacy: legacyWithPublish})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assertAbsent(t, target, rpCaller)
	assert.Equal(t, "custom-release-please", mustAction(t, actions, rpCaller).Reason)
	assert.Equal(t, "keep", mustAction(t, actions, legacy).Action)
	assert.Equal(t, legacyWithPublish, readRel(t, target, legacy))
}

func TestRP_Migrate_Uncommitted_Refused(t *testing.T) {
	target := gitRepoWith(t, map[string]string{rpCaller: legacyAppToken})
	edited := legacyAppToken + "# local tweak\n"
	writeRel(t, target, rpCaller, edited)
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	a := mustAction(t, actions, rpCaller)
	assert.Equal(t, "suggest-sibling", a.Action)
	assert.Equal(t, "uncommitted-release-please", a.Reason)
	assert.Equal(t, edited, readRel(t, target, rpCaller))
}

func TestRP_Migrate_Untracked_Refused(t *testing.T) {
	target := gitRepoWith(t, map[string]string{"README.md": "x\n"})
	writeRel(t, target, rpCaller, legacyAppToken)
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "uncommitted-release-please", mustAction(t, actions, rpCaller).Reason)
	assert.Equal(t, legacyAppToken, readRel(t, target, rpCaller))
}

func TestRP_Migrate_NotAGitRepo_Refused(t *testing.T) {
	target := t.TempDir()
	writeRel(t, target, rpCaller, legacyAppToken)
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "uncommitted-release-please", mustAction(t, actions, rpCaller).Reason)
	assert.Equal(t, legacyAppToken, readRel(t, target, rpCaller))
}

func TestRP_Migrate_TwoLegacyWorkflows_Refused(t *testing.T) {
	target := gitRepoWith(t, map[string]string{
		rpCaller:                        legacyAppToken,
		".github/workflows/release.yml": legacyRootConfig,
	})
	actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "suggest-sibling", mustAction(t, actions, rpCaller).Action)
	assert.Equal(t, legacyAppToken, readRel(t, target, rpCaller))
	assert.Equal(t, legacyRootConfig, readRel(t, target, ".github/workflows/release.yml"))
}

func TestRP_Migrate_UnknownStepInput_IsCustom(t *testing.T) {
	for _, extra := range []string{
		"skip-github-release: true", // non-string value
		"release-type: go",          // string value
	} {
		body := legacyPAT + "          " + extra + "\n"
		target := gitRepoWith(t, map[string]string{rpCaller: body})
		actions, err := renderReleasePlease(target, migrateInputs(), fixedNow())
		require.NoError(t, err)
		a := mustAction(t, actions, rpCaller)
		assert.Equal(t, "custom-release-please", a.Reason, extra)
		assert.Contains(t, a.Detail, extra[:strings.Index(extra, ":")], extra)
		assert.Equal(t, body, readRel(t, target, rpCaller), extra)
	}
}

// --- a managed caller plus a leftover hand-written workflow -----------------------

func TestRP_ManagedCallerPlusDuplicate_ReportedThenRemovedOnMigrate(t *testing.T) {
	target := gitRepoWith(t, map[string]string{"README.md": "x\n"})
	_, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	legacy := ".github/workflows/release.yml"
	writeRel(t, target, legacy, legacyAppToken)
	rpGit(t, target, "add", "-A")
	rpGit(t, target, "commit", "-q", "-m", "dup")

	actions, err := renderReleasePlease(target, rpInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "skip-unchanged", mustAction(t, actions, rpCaller).Action)
	k := mustAction(t, actions, legacy)
	assert.Equal(t, "keep", k.Action)
	assert.Equal(t, "duplicate-release-please", k.Reason)

	actions, err = renderReleasePlease(target, migrateInputs(), fixedNow())
	require.NoError(t, err)
	assert.Equal(t, "remove", mustAction(t, actions, legacy).Action)
	_, statErr := os.Stat(filepath.Join(target, legacy))
	assert.True(t, os.IsNotExist(statErr))
}

// callerSettingsLoose reads settings from a rendered caller that is not
// (yet) the live file, e.g. a .kit-suggested sibling.
func callerSettingsLoose(t *testing.T, content string) releasePleaseSettings {
	t.Helper()
	wf, isRP := classifyReleasePleaseWorkflow(rpCaller, []byte(content))
	require.True(t, isRP)
	require.True(t, wf.Plain, wf.Why)
	return wf.Settings
}
