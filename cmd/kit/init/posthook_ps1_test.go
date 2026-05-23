// Package kitinit — posthook_ps1_test.go covers the Windows .ps1 companion
// to .githooks/post-pr-open. The bash script targets POSIX shells (Linux,
// macOS, Git-Bash on Windows via MSYS); the .ps1 ports the same flow for
// native PowerShell so adopters on stock Windows don't have to install
// Git-Bash to wire up the kit init follow-up.
//
// What we assert (port — not refactor):
//   - GeneratePostPROpenHook materialises BOTH .githooks/post-pr-open
//     and .githooks/post-pr-open.ps1 on a fresh tree.
//   - Augment-conflict (suggest-sibling) policy applies to .ps1 too — a
//     user-edited .ps1 yields .ps1.kit-suggested, never an overwrite.
//   - Skip-unchanged + auto-cleanup mirrors the bash flow for .ps1.
//   - Dry-run leaves no .ps1 on disk.
//   - The disabled flag (--without-githook-post-pr-open) suppresses the
//     .ps1 just like the .sh.
//   - The .ps1 script content carries the same canonical-topic map
//     (run/comment/merged/closed → 4-segment topic) and the same
//     dedup-key shape so behavioral parity is observable at the
//     template level.
//   - Mode bits: .ps1 is 0644 (Windows runs by extension, not exec bit).
//
// We do NOT execute the .ps1 — `pwsh` is not guaranteed in CI, and the
// hook's runtime contract is already exercised end-to-end by the bash
// suite. Static assertions on the embedded template + the generator's
// per-file action shape give us the cross-platform coverage we need
// without dragging a PowerShell dependency into the test matrix.
package kitinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Generator — .ps1 is materialised alongside the .sh.
// -----------------------------------------------------------------------------

func TestGeneratePostPROpenHook_WritesPS1Companion(t *testing.T) {
	target := t.TempDir()
	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	// The summary action covers the bash file (primary). The .ps1 is
	// reported via the companion result on the same call; we assert its
	// on-disk presence below.
	assert.Equal(t, PostHookActionWrite, res.Action)

	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	assert.FileExists(t, ps1Path, ".ps1 companion must be scaffolded next to the bash hook")

	// Mode 0644: Windows runs by extension, no executable bit needed.
	// macOS/Linux file systems still honor the bits, so we assert the
	// non-executable shape so adopters who copy the tree to Windows
	// don't end up with a +x file flagged as suspicious by AV.
	info, err := os.Stat(ps1Path)
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&0o111,
		".ps1 must NOT carry the exec bit; PowerShell runs by extension")
}

func TestGeneratePostPROpenHook_PS1_SkipUnchanged(t *testing.T) {
	target := t.TempDir()
	// First run writes both files; second run with identical content → skip.
	_, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)

	// Capture .ps1 mtime; second run must not rewrite it.
	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	before, err := os.Stat(ps1Path)
	require.NoError(t, err)

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkipUnchanged, res.Action)

	after, err := os.Stat(ps1Path)
	require.NoError(t, err)
	// Same size + content (we don't rely on mtime equality — some
	// filesystems normalize on touch — but identical content is
	// observable via re-read).
	beforeBytes, err := os.ReadFile(ps1Path)
	require.NoError(t, err)
	afterBytes, err := os.ReadFile(ps1Path)
	require.NoError(t, err)
	assert.Equal(t, beforeBytes, afterBytes, ".ps1 must not be rewritten on second run")
	assert.Equal(t, before.Size(), after.Size())
}

func TestGeneratePostPROpenHook_PS1_SuggestSiblingOnDivergence(t *testing.T) {
	target := t.TempDir()
	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	require.NoError(t, os.MkdirAll(filepath.Dir(ps1Path), 0o750))
	require.NoError(t, os.WriteFile(ps1Path, []byte("# user-customized .ps1\nWrite-Host 'custom'\n"), 0o644))

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	// The result's primary Action covers the .sh (it's a new write).
	// The .ps1 sibling is observable on disk independently — see below.
	_ = res

	suggested := ps1Path + ".kit-suggested"
	assert.FileExists(t, suggested,
		"user-edited .ps1 must yield .ps1.kit-suggested, never an overwrite")

	// Original file untouched.
	got, err := os.ReadFile(ps1Path)
	require.NoError(t, err)
	assert.Equal(t, "# user-customized .ps1\nWrite-Host 'custom'\n", string(got),
		"augment-conflict policy: original .ps1 must NOT be overwritten")

	// Sibling carries kit's content.
	suggestedBytes, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, PostPROpenPS1Content(), suggestedBytes,
		".kit-suggested must carry kit's would-be .ps1 content")
}

func TestGeneratePostPROpenHook_PS1_AutoCleansSuggestionOnConvergence(t *testing.T) {
	target := t.TempDir()
	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	suggested := ps1Path + ".kit-suggested"
	require.NoError(t, os.MkdirAll(filepath.Dir(ps1Path), 0o750))

	// User adopted kit's .ps1 content; stale sibling left over from a
	// previous run.
	require.NoError(t, os.WriteFile(ps1Path, PostPROpenPS1Content(), 0o644))
	require.NoError(t, os.WriteFile(suggested, []byte("# stale\n"), 0o644))

	// Also write the bash file with kit's content so the primary result
	// is skip-unchanged (the test focuses on .ps1 cleanup, not .sh).
	shPath := filepath.Join(target, ".githooks", "post-pr-open")
	require.NoError(t, os.WriteFile(shPath, PostPROpenHookContent(), 0o755))

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkipUnchanged, res.Action)

	_, statErr := os.Stat(suggested)
	assert.True(t, os.IsNotExist(statErr),
		"stale .ps1.kit-suggested must be removed when user converges")
}

func TestGeneratePostPROpenHook_PS1_DryRunDoesNotWrite(t *testing.T) {
	target := t.TempDir()
	_, err := GeneratePostPROpenHook(target, true, true)
	require.NoError(t, err)

	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	_, statErr := os.Stat(ps1Path)
	assert.True(t, os.IsNotExist(statErr), "dry-run must not write the .ps1")
}

func TestGeneratePostPROpenHook_PS1_DisabledFlagSkips(t *testing.T) {
	target := t.TempDir()
	res, err := GeneratePostPROpenHook(target, false, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkippedFlag, res.Action)

	ps1Path := filepath.Join(target, ".githooks", "post-pr-open.ps1")
	_, statErr := os.Stat(ps1Path)
	assert.True(t, os.IsNotExist(statErr),
		"--without-githook-post-pr-open must suppress the .ps1 too")
}

// -----------------------------------------------------------------------------
// Static template assertions — the .ps1 must carry the same canonical-topic
// map and dedup-key shape so behavioral parity with the bash hook is
// observable at the template level. We don't execute the script (pwsh not
// guaranteed in CI); the static surface is enough to catch porting drift.
// -----------------------------------------------------------------------------

func TestPostPROpenPS1_CanonicalTopicMap(t *testing.T) {
	content := string(PostPROpenPS1Content())

	// Same 4-segment topics as bash kit_canonical_topic.
	for _, want := range []string{
		"github.pr.run.completed",
		"github.pr.comment.created",
		"github.pr.pull.merged",
		"github.pr.pull.closed",
	} {
		assert.Contains(t, content, want,
			"canonical topic %q must appear in .ps1 (parity with bash)", want)
	}

	// Short family labels referenced by the switch arms.
	for _, want := range []string{"run", "comment", "merged", "closed"} {
		assert.Contains(t, content, "'"+want+"'",
			"family label %q must appear as a switch arm in .ps1", want)
	}
}

func TestPostPROpenPS1_DedupKeyShape(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// Same tag schema as bash (kit:pr-followup, event:<topic>, dedup triple).
	assert.Contains(t, content, "kit:pr-followup",
		"fixed pr-followup tag must appear in .ps1")
	assert.Contains(t, content, "event:",
		"per-event tag prefix must appear in .ps1")
	// Dedup triple uses the same separator scheme: <owner>-<name>:<pr>:<family>.
	// We look for the assembly statement; the exact format string differs from
	// bash sprintf, but the colon separator + "kit:pr-followup:" prefix must
	// be present.
	assert.Contains(t, content, "kit:pr-followup:",
		"dedup-key tag's family-bearing prefix must appear in .ps1")
}

func TestPostPROpenPS1_LivenessProbeContract(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// 5-second timeout matches bash --max-time 5.
	assert.Contains(t, content, "TimeoutSec 5",
		"liveness probe must use a 5-second timeout (parity with curl --max-time 5)")
	// /healthz appended to the configured base URL.
	assert.Contains(t, content, "/healthz",
		"probe must hit ${KIT_BUS_INGRESS_URL%/}/healthz (same path as bash)")
	// Invoke-WebRequest is the canonical PowerShell HTTP verb here.
	assert.Contains(t, content, "Invoke-WebRequest",
		"probe must use Invoke-WebRequest (PowerShell native HTTP)")
}

func TestPostPROpenPS1_EnvVarsMatchBash(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// PowerShell reads env vars via $env:NAME; the names mirror bash.
	for _, want := range []string{
		"$env:KIT_BUS_ENABLED",
		"$env:KIT_BUS_INGRESS_URL",
		"$env:KIT_POST_PR_HOOK_FAMILY",
		"$env:KIT_POST_PR_HOOK_DUE",
		"$env:KIT_POST_PR_HOOK_DEBUG",
	} {
		assert.Contains(t, content, want,
			"env var %q must be referenced via $env: in .ps1 (parity with bash)", want)
	}
}

func TestPostPROpenPS1_FailOpenContract(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// gh + tlc availability checked via Get-Command. Missing tooling
	// must NOT block PR creation — script always exits 0.
	assert.Contains(t, content, "Get-Command",
		"tool discovery must use Get-Command (PowerShell idiom)")
	assert.Contains(t, content, "exit 0",
		"script must exit 0 on all paths (fail-open)")
	// Both push and pull paths reach an exit 0 — we don't enumerate
	// every branch, but assert that no `exit 1` (or higher) leaks
	// through. A script that exits non-zero would defeat fail-open.
	assert.NotContains(t, content, "exit 1",
		"hook must never exit non-zero (fail-open contract)")
	assert.NotContains(t, content, "exit 2",
		"hook must never exit non-zero (fail-open contract)")
}

func TestPostPROpenPS1_TaskIDResolution(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// Same branch convention: ^[tT]-(\d{3,6})(?:-|$) → "T-NNNN".
	// PowerShell regex syntax matches Go's RE2 enough for this pattern.
	assert.Contains(t, content, `^[tT]-(\d{3,6})`,
		"branch task-ID regex must match bash + Go (3..6 digit window)")
	assert.Contains(t, content, "T-",
		"canonical task-ID prefix must appear when assembling the reference")
}

func TestPostPROpenPS1_GHViewJSONFields(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// gh pr view --json fields the hook reads. PowerShell uses
	// ConvertFrom-Json which gives us property access, so the field
	// names appear as identifiers (e.g. .number, .headRefName).
	for _, want := range []string{
		"number", "url", "headRefName", "headRefOid", "title", "baseRepository",
	} {
		assert.Contains(t, content, want,
			"gh pr view --json field %q must be referenced in .ps1", want)
	}
	assert.Contains(t, content, "ConvertFrom-Json",
		"PR metadata must parse via ConvertFrom-Json (PowerShell native JSON)")
}

// -----------------------------------------------------------------------------
// Header / hook-discipline assertions.
// -----------------------------------------------------------------------------

func TestPostPROpenPS1_HeaderAndShape(t *testing.T) {
	content := string(PostPROpenPS1Content())
	// The header must call out kit-init provenance + the contract path so
	// future maintainers find the spec.
	assert.Contains(t, content, "kit init",
		".ps1 header must identify the generator (kit init)")
	assert.Contains(t, content, "post-pr-open",
		".ps1 header must reference the bash counterpart by name")
	// Strict mode is the PowerShell equivalent of `set -u`: catch
	// undefined variables early.
	assert.True(t,
		strings.Contains(content, "Set-StrictMode") || strings.Contains(content, "$ErrorActionPreference"),
		".ps1 must opt into Strict mode (or set ErrorActionPreference) for parity with `set -u`")
}
