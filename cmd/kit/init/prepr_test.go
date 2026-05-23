// prepr_test.go covers T-0773: the before-PR git hook scaffolder, its
// manifest, and the slug/scratchpad-path helpers. Scope:
//
//   - GeneratePrePrHook on a fresh tree → write + manifest.
//   - GeneratePrePrHook on an unchanged tree → skip-unchanged + manifest skip.
//   - GeneratePrePrHook on a user-edited tree → suggest-sibling + manifest.
//   - GeneratePrePrHook on a stale-suggestion tree → sibling cleanup.
//   - Dry-run mode → reports actions but writes nothing.
//   - ProjectIDSlug / DeriveSlugFromOrigin / DeriveSlugFromPath against the
//     contract Section 4 worked examples.
//   - ScratchpadPath for darwin / linux (with and without XDG_RUNTIME_DIR)
//     / windows, via injected env + GOOS.
//   - ScanScratchpad positive + negative cases.
//   - The embedded shell script is well-formed (parses with `bash -n`)
//     and resolves each gate from Makefile / mise.toml / .kit/pre-pr.toml
//     in the documented order (gated behind `bash` and `make` on PATH so
//     CI runners without them skip cleanly).
//   - Exit semantics: failing each gate produces the documented stderr
//     prefix and a non-zero exit code.
package kitinit

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedTime returns a deterministic timestamp so manifest equality
// assertions don't drift between assertions.
func fixedTime() time.Time {
	return time.Date(2026, 5, 23, 14, 0, 0, 0, time.UTC)
}

func TestGeneratePrePrHook_FreshTree(t *testing.T) {
	root := t.TempDir()

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)
	require.Len(t, res.Files, 2)

	// Hook row.
	assert.Equal(t, PrePrHookPath, res.Files[0].Path)
	assert.Equal(t, ActionWrite, res.Files[0].Action)
	assert.Equal(t, ReasonNew, res.Files[0].Reason)

	// Manifest row.
	assert.Equal(t, GeneratedManifestPath, res.Files[1].Path)
	assert.Equal(t, ActionManifestUpdate, res.Files[1].Action)

	// Hook is executable.
	info, err := os.Stat(filepath.Join(root, PrePrHookPath))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100,
		"hook must be executable; got mode %v", info.Mode().Perm())

	// Manifest carries one entry for the hook with the right hash.
	m, err := ReadGeneratedManifest(root)
	require.NoError(t, err)
	require.Equal(t, GeneratedManifestVersion, m.Version)
	assert.Equal(t, GeneratedManifestProducer, m.GeneratedBy)
	require.Len(t, m.Files, 1)
	assert.Equal(t, PrePrHookPath, m.Files[0].Path)

	hookBytes, _ := loadPrePrHookBytes()
	assert.Equal(t, hashBytes(hookBytes), m.Files[0].SHA256)
}

func TestGeneratePrePrHook_SkipUnchanged(t *testing.T) {
	root := t.TempDir()

	// First run: writes the hook + manifest.
	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	// Second run with the same timestamp: bytes identical → skip-unchanged
	// for the hook and skip-unchanged for the manifest.
	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)
	require.Len(t, res.Files, 2)
	assert.Equal(t, ActionSkipUnchanged, res.Files[0].Action)
	assert.Equal(t, ActionSkipUnchanged, res.Files[1].Action,
		"manifest re-write with identical bytes must be skip-unchanged")
}

func TestGeneratePrePrHook_UserEdited(t *testing.T) {
	root := t.TempDir()

	// Seed an existing hook with custom content NOT in the manifest.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	custom := []byte("#!/usr/bin/env bash\n# user-edited\nexit 0\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, PrePrHookPath), custom, 0o755))

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hookReport := res.Files[0]
	assert.Equal(t, ActionSuggestSibling, hookReport.Action)
	assert.Equal(t, ReasonUserEdited, hookReport.Reason)
	assert.Equal(t, PrePrHookPath+".kit-suggested", hookReport.SuggestedPath)

	// Original untouched.
	got, err := os.ReadFile(filepath.Join(root, PrePrHookPath))
	require.NoError(t, err)
	assert.Equal(t, string(custom), string(got))

	// Sibling materialised with kit content.
	hookBytes, _ := loadPrePrHookBytes()
	sibling, err := os.ReadFile(filepath.Join(root, hookReport.SuggestedPath))
	require.NoError(t, err)
	assert.Equal(t, string(hookBytes), string(sibling))
}

func TestGeneratePrePrHook_RefreshInPlace(t *testing.T) {
	root := t.TempDir()
	hookBytes, _ := loadPrePrHookBytes()

	// Seed: write an older copy of the hook + a matching manifest entry.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	older := append([]byte("# older variant\n"), hookBytes...)
	require.NoError(t, os.WriteFile(filepath.Join(root, PrePrHookPath), older, 0o755))

	m := GeneratedManifest{
		Version:     GeneratedManifestVersion,
		GeneratedBy: GeneratedManifestProducer,
		Files: []GeneratedManifestFile{{
			Path:        PrePrHookPath,
			SHA256:      hashBytes(older),
			GeneratedAt: fixedTime(),
		}},
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	mb = append(mb, '\n')
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, GeneratedManifestPath), mb, 0o644))

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hookReport := res.Files[0]
	assert.Equal(t, ActionWrite, hookReport.Action)
	assert.Equal(t, ReasonRefresh, hookReport.Reason)

	// File now matches the canonical hook bytes.
	got, err := os.ReadFile(filepath.Join(root, PrePrHookPath))
	require.NoError(t, err)
	assert.Equal(t, string(hookBytes), string(got))
}

func TestGeneratePrePrHook_SuggestionCleanup(t *testing.T) {
	root := t.TempDir()
	hookBytes, _ := loadPrePrHookBytes()

	// Seed: the user has copied the previously-suggested sibling into
	// place verbatim (the on-disk file matches the sibling). Section 6
	// says the sibling must be removed before any new decision.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, PrePrHookPath), hookBytes, 0o755))
	suggested := filepath.Join(root, PrePrHookPath+".kit-suggested")
	require.NoError(t, os.WriteFile(suggested, hookBytes, 0o644))

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	_, statErr := os.Stat(suggested)
	assert.True(t, os.IsNotExist(statErr),
		"byte-identical .kit-suggested sibling must be cleaned up; stat err=%v", statErr)
}

// TestGeneratePrePrHook_PropagatesManifestReadError guards Fix 2:
// GeneratePrePrHook used to swallow the error from ReadGeneratedManifest
// with `manifest, _ := ...`. ReadGeneratedManifest already converts safe
// cases (os.IsNotExist, malformed JSON) to (zero, nil), so propagating
// its error only surfaces genuine I/O failures (chmod 0, etc.) that
// the caller has no other way to learn about.
// TestGeneratePrePrHook_InstallsPs1Companion guards Fix 5 Parts B+C:
// alongside the bash hook the generator must also install a PowerShell
// companion at .githooks/pre-pr.ps1 (mode 0644 — Windows runs by
// extension, not by exec bit) and add a manifest entry for it.
func TestGeneratePrePrHook_InstallsPs1Companion(t *testing.T) {
	root := t.TempDir()

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	// Result rows: bash hook, ps1 hook, manifest.
	paths := make(map[string]PrePrFileReport, len(res.Files))
	for _, r := range res.Files {
		paths[r.Path] = r
	}
	require.Contains(t, paths, PrePrHookPath, "bash hook must be reported")
	require.Contains(t, paths, PrePrHookPs1Path,
		"PowerShell companion must be reported in result")
	assert.Equal(t, ActionWrite, paths[PrePrHookPs1Path].Action,
		"ps1 must be a fresh write on an empty tree")

	// On-disk ps1 exists with reasonable mode (0644).
	ps1Abs := filepath.Join(root, PrePrHookPs1Path)
	info, err := os.Stat(ps1Abs)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		".ps1 must be mode 0644 (no exec bit; Windows runs by extension)")

	// Manifest carries the ps1 entry.
	m, err := ReadGeneratedManifest(root)
	require.NoError(t, err)
	var foundPs1 bool
	for _, f := range m.Files {
		if f.Path == PrePrHookPs1Path {
			foundPs1 = true
			ps1Bytes, _ := loadPrePrHookPs1Bytes()
			assert.Equal(t, hashBytes(ps1Bytes), f.SHA256,
				"manifest hash must match the ps1 asset bytes")
		}
	}
	assert.True(t, foundPs1, "manifest must include the .ps1 path")
}

// TestPrePrHookPs1Asset_StructuralMarkers guards the embedded ps1
// content shape: the same gates and exit codes as the bash hook, the
// same scratchpad markers, and a slug derivation function. Tests here
// are static (string-scan) so they run even when pwsh is unavailable
// in CI; a separate test exercises pwsh end-to-end when present.
func TestPrePrHookPs1Asset_StructuralMarkers(t *testing.T) {
	body, err := loadPrePrHookPs1Bytes()
	require.NoError(t, err)
	got := string(body)

	// Exit semantics parity with bash hook.
	assert.Contains(t, got, "exit 1", "ps1 must exit 1 on lint failure")
	assert.Contains(t, got, "exit 2", "ps1 must exit 2 on test failure")
	assert.Contains(t, got, "exit 3", "ps1 must exit 3 on scratchpad failure")

	// Scratchpad patterns parity.
	for _, p := range ScratchpadPatterns {
		assert.Contains(t, got, p,
			"ps1 must scan for marker %q", p)
	}

	// Gate resolution: at minimum it has to mention Makefile, mise, and
	// .kit/pre-pr.toml in some order.
	assert.Contains(t, got, "Makefile")
	assert.Contains(t, got, "mise")
	assert.Contains(t, got, "pre-pr.toml")

	// Scratchpad path derivation references LOCALAPPDATA.
	assert.Contains(t, got, "LOCALAPPDATA",
		"ps1 must derive scratchpad path from LOCALAPPDATA on Windows")
}

// TestPrePrHookPs1Asset_PwshSyntax does a pwsh -NoProfile -Command
// syntax check on the embedded ps1 if pwsh is available. CI runners
// without pwsh skip; the structural test above is the baseline.
func TestPrePrHookPs1Asset_PwshSyntax(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not on PATH; structural test covers asset shape")
	}
	body, err := loadPrePrHookPs1Bytes()
	require.NoError(t, err)
	tmp := filepath.Join(t.TempDir(), "pre-pr.ps1")
	require.NoError(t, os.WriteFile(tmp, body, 0o644))

	// `pwsh -NoProfile -Command "& { . <file>; exit 99 }"` would
	// execute. We just want a parse, so use the AST parser.
	parser := `try {
  $tokens = $null; $errors = $null
  [System.Management.Automation.Language.Parser]::ParseFile(
    '` + tmp + `', [ref]$tokens, [ref]$errors) | Out-Null
  if ($errors.Count -gt 0) {
    $errors | ForEach-Object { Write-Error $_.Message }
    exit 1
  }
} catch { Write-Error $_.Exception.Message; exit 1 }`

	out, runErr := exec.Command(pwsh, "-NoProfile", "-Command", parser).
		CombinedOutput()
	require.NoError(t, runErr, "ps1 must parse cleanly:\n%s", out)
}

// TestGeneratePrePrHook_Ps1SuggestSibling guards the augment policy
// for the .ps1 file: a hand-edited ps1 must route to .kit-suggested
// the same way the bash hook does.
func TestGeneratePrePrHook_Ps1SuggestSibling(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	custom := []byte("# user-edited ps1\nWrite-Host 'hi'\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(root, PrePrHookPs1Path), custom, 0o644))

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	var ps1Row *PrePrFileReport
	for i := range res.Files {
		if res.Files[i].Path == PrePrHookPs1Path {
			ps1Row = &res.Files[i]
		}
	}
	require.NotNil(t, ps1Row, "ps1 report row missing")
	assert.Equal(t, ActionSuggestSibling, ps1Row.Action)
	assert.Equal(t, PrePrHookPs1Path+".kit-suggested", ps1Row.SuggestedPath)

	// Original untouched.
	got, err := os.ReadFile(filepath.Join(root, PrePrHookPs1Path))
	require.NoError(t, err)
	assert.Equal(t, string(custom), string(got))
}

func TestGeneratePrePrHook_PropagatesManifestReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod(0) semantics differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses unix mode bits; cannot exercise read-failure")
	}

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	abs := filepath.Join(root, GeneratedManifestPath)
	require.NoError(t, os.WriteFile(abs, []byte(`{"version":1}`), 0o644))
	require.NoError(t, os.Chmod(abs, 0))
	t.Cleanup(func() {
		// Restore so t.TempDir cleanup can remove the file.
		_ = os.Chmod(abs, 0o644)
	})

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.Error(t, err, "real manifest read errors must propagate")
	assert.Contains(t, err.Error(), "read manifest",
		"error should wrap the manifest read failure")
}

// TestGeneratePrePrHook_SkipUnchangedFixesMode guards Fix 3: when the
// hook is already present with the canonical bytes but wrong mode
// (e.g. 0644 — clone from a fileshare that drops the +x bit, or a
// previous run on a filesystem that didn't preserve the executable
// bit), kit init must still chmod it. Otherwise the action reports
// "skip-unchanged" but git refuses to run the hook.
func TestGeneratePrePrHook_SkipUnchangedFixesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix exec-bit semantics; windows uses extension association")
	}
	root := t.TempDir()
	hookBytes, _ := loadPrePrHookBytes()

	// Seed: canonical content, wrong mode.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	abs := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.WriteFile(abs, hookBytes, 0o644))

	res, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)
	require.Len(t, res.Files, 2)

	assert.Equal(t, ActionSkipUnchanged, res.Files[0].Action,
		"identical bytes should still report skip-unchanged")

	info, err := os.Stat(abs)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100,
		"skip-unchanged path must still chmod the hook executable; got mode %v",
		info.Mode().Perm())
}

// TestGeneratePrePrHook_RefreshInPlaceFixesMode guards the refresh
// branch for the same defect — the canonical-content path is the
// hottest case but `manifestHashMatches → refresh` shares the same
// gap.
func TestGeneratePrePrHook_RefreshInPlaceFixesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix exec-bit semantics; windows uses extension association")
	}
	root := t.TempDir()
	hookBytes, _ := loadPrePrHookBytes()

	// Seed an older copy that matches the manifest but isn't executable.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".githooks"), 0o755))
	older := append([]byte("# older variant\n"), hookBytes...)
	abs := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.WriteFile(abs, older, 0o644))

	m := GeneratedManifest{
		Version:     GeneratedManifestVersion,
		GeneratedBy: GeneratedManifestProducer,
		Files: []GeneratedManifestFile{{
			Path:        PrePrHookPath,
			SHA256:      hashBytes(older),
			GeneratedAt: fixedTime(),
		}},
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	mb = append(mb, '\n')
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, GeneratedManifestPath), mb, 0o644))

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	info, err := os.Stat(abs)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100,
		"refresh path must chmod the hook executable; got mode %v",
		info.Mode().Perm())
}

func TestGeneratePrePrHook_DryRunWritesNothing(t *testing.T) {
	root := t.TempDir()

	res, err := GeneratePrePrHook(root, true, fixedTime())
	require.NoError(t, err)
	require.Len(t, res.Files, 2)
	// Report still describes the would-be actions.
	assert.Equal(t, ActionWrite, res.Files[0].Action)
	assert.Equal(t, ActionManifestUpdate, res.Files[1].Action)

	for _, rel := range []string{PrePrHookPath, GeneratedManifestPath} {
		_, statErr := os.Stat(filepath.Join(root, rel))
		assert.True(t, os.IsNotExist(statErr),
			"dry-run wrote %s; stat err=%v", rel, statErr)
	}
}

func TestDeriveSlugFromOrigin_WorkedExamples(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Contract Section 4 worked examples.
		{"git@github.com:hop-top/poly-kit.git", "github-com-hop-top-poly-kit"},
		{"https://github.com/hop-top/poly-kit.git", "github-com-hop-top-poly-kit"},
		{"https://gitea.example.org/team/Repo.Name", "gitea-example-org-team-repo-name"},
		// scheme variants.
		{"ssh://git@github.com/hop-top/poly-kit.git", "github-com-hop-top-poly-kit"},
		{"http://gitea.example.org/team/Repo.Name", "gitea-example-org-team-repo-name"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := DeriveSlugFromOrigin(c.in)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestDeriveSlugFromPath_WorkedExample(t *testing.T) {
	// Contract Section 4: "/Users/jad/work/My Project" → "users-jad-work-my-project".
	got := DeriveSlugFromPath("/Users/jad/work/My Project")
	assert.Equal(t, "users-jad-work-my-project", got)
}

func TestProjectIDSlug_FallbackToPath(t *testing.T) {
	// Bare temp dir → no .git → no origin → path fallback. The path is
	// per-user (t.TempDir under $TMPDIR), so we only assert the slug is
	// non-empty and contains the temp basename, not a literal.
	root := t.TempDir()
	got := ProjectIDSlug(root)
	assert.NotEmpty(t, got)
	// Slug rules: lowercase, only [a-z0-9-].
	for _, r := range got {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		assert.True(t, ok, "slug rune %q must be in [a-z0-9-]; slug=%q", r, got)
	}
}

func TestProjectIDSlug_EmptyFallsBackToLiteral(t *testing.T) {
	got := DeriveSlugFromOrigin("")
	assert.Empty(t, got, "empty origin yields empty slug; caller falls back")

	// Path slug for "/" trims to empty, which the public entrypoint
	// would replace with "kit-init".
	got = DeriveSlugFromPath("/")
	assert.Empty(t, got)
}

func TestScratchpadPath_MacOS_TMPDIRSet(t *testing.T) {
	env := mapEnv(map[string]string{"TMPDIR": "/var/folders/xx/yy/T/"})
	got := ScratchpadPath("github-com-acme-foo", "darwin", env)
	// filepath.Join collapses trailing slashes.
	assert.Equal(t, filepath.Join("/var/folders/xx/yy/T", "github-com-acme-foo.scratchpad"), got)
}

func TestScratchpadPath_MacOS_TMPDIRUnset(t *testing.T) {
	env := mapEnv(map[string]string{})
	got := ScratchpadPath("github-com-acme-foo", "darwin", env)
	assert.Equal(t, "/tmp/github-com-acme-foo.scratchpad", got)
}

func TestScratchpadPath_Linux_XDGSet(t *testing.T) {
	env := mapEnv(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"})
	got := ScratchpadPath("github-com-acme-foo", "linux", env)
	assert.Equal(t, "/run/user/1000/github-com-acme-foo.scratchpad", got)
}

func TestScratchpadPath_Linux_XDGUnset_TMPDIRSet(t *testing.T) {
	env := mapEnv(map[string]string{"TMPDIR": "/var/tmp"})
	got := ScratchpadPath("github-com-acme-foo", "linux", env)
	assert.Equal(t, "/var/tmp/github-com-acme-foo.scratchpad", got)
}

func TestScratchpadPath_Linux_AllUnset(t *testing.T) {
	env := mapEnv(map[string]string{})
	got := ScratchpadPath("github-com-acme-foo", "linux", env)
	assert.Equal(t, "/tmp/github-com-acme-foo.scratchpad", got)
}

func TestScratchpadPath_Windows(t *testing.T) {
	env := mapEnv(map[string]string{"LOCALAPPDATA": `C:\Users\jad\AppData\Local`})
	got := ScratchpadPath("github-com-acme-foo", "windows", env)
	// filepath.Join on a non-Windows host normalises separators; assert
	// on the leaf shape instead.
	assert.Contains(t, got, "github-com-acme-foo.scratchpad")
	assert.Contains(t, got, "Temp")
}

// TestScratchpadPath_Windows_FallbackPaths_NoDoubledTemp guards the
// three Windows env states described by the Section 4 fallback chain
// (LOCALAPPDATA → TEMP → C:\Windows\Temp). The bug being asserted
// against: a naive `filepath.Join(base, "Temp", leaf)` doubles Temp
// when base already contains it (TEMP env var, or the C:\Windows\Temp
// hard fallback).
func TestScratchpadPath_Windows_FallbackPaths_NoDoubledTemp(t *testing.T) {
	t.Run("LOCALAPPDATA set", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"LOCALAPPDATA": `C:\Users\foo\AppData\Local`,
			"TEMP":         "",
		})
		got := ScratchpadPath("acme", "windows", env)
		want := filepath.Join(`C:\Users\foo\AppData\Local`, "Temp", "acme.scratchpad")
		assert.Equal(t, want, got)
	})

	t.Run("LOCALAPPDATA unset, TEMP set", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"LOCALAPPDATA": "",
			"TEMP":         `C:\Users\foo\AppData\Local\Temp`,
		})
		got := ScratchpadPath("acme", "windows", env)
		// TEMP already includes the Temp segment; must not double it.
		want := filepath.Join(`C:\Users\foo\AppData\Local\Temp`, "acme.scratchpad")
		assert.Equal(t, want, got, "TEMP fallback must not append a doubled Temp segment")
	})

	t.Run("LOCALAPPDATA and TEMP unset", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"LOCALAPPDATA": "",
			"TEMP":         "",
		})
		got := ScratchpadPath("acme", "windows", env)
		// C:\Windows\Temp already includes Temp; must not double it.
		want := filepath.Join(`C:\Windows\Temp`, "acme.scratchpad")
		assert.Equal(t, want, got, "hard fallback must not append a doubled Temp segment")
	})
}

func TestScanScratchpad_PositiveCases(t *testing.T) {
	cases := []string{
		"// SCRATCH: write a parser here later",
		"# FIXME-PLAN: split into two steps",
		"// AGENT-NOTE: not sure about this branch",
		"// KIT-SCRATCH: drafted but unverified",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			assert.True(t, ScanScratchpad([]byte(c)))
		})
	}
}

func TestScanScratchpad_NegativeCases(t *testing.T) {
	cases := []string{
		"// regular comment",
		"const SCRATCHPAD = 1 // identifier-only, no colon",
		"// TODO: real production todo",
		"const message = \"FIXME later\"", // no -PLAN: suffix
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			assert.False(t, ScanScratchpad([]byte(c)))
		})
	}
}

func TestPrePrAssetEmbeddedHook_ParsesAsBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	hookBytes, err := loadPrePrHookBytes()
	require.NoError(t, err)

	tmp := filepath.Join(t.TempDir(), "pre-pr")
	require.NoError(t, os.WriteFile(tmp, hookBytes, 0o755))

	out, err := exec.Command("bash", "-n", tmp).CombinedOutput()
	require.NoError(t, err, "bash -n failed:\n%s", out)
}

func TestHookGateResolution_MakefilePreferred(t *testing.T) {
	// Verify the embedded hook resolves lint via Makefile when one is
	// present with a `lint` target. We run the hook in a tempdir with a
	// Makefile whose `lint` target succeeds and a stub `test` target.
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	// Initialise git so the hook's `git ls-files` does not error.
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	require.NoError(t, os.WriteFile(filepath.Join(root, "Makefile"), []byte(
		"lint:\n\t@echo lint-ok\n"+
			"test:\n\t@echo test-ok\n"),
		0o644))
	mustRun(t, root, "git", "add", "Makefile")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "hook exit non-zero with Makefile gates:\n%s", out)
	assert.Contains(t, string(out), "lint-ok")
	assert.Contains(t, string(out), "test-ok")
}

func TestHookGateResolution_KitTomlFallback(t *testing.T) {
	// No Makefile / mise → .kit/pre-pr.toml provides the commands.
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".kit", "pre-pr.toml"),
		[]byte(`lint = "echo kit-lint-ok"`+"\n"+
			`test = "echo kit-test-ok"`+"\n"), 0o644))
	mustRun(t, root, "git", "add", ".kit/pre-pr.toml")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "hook exit non-zero with .kit/pre-pr.toml gates:\n%s", out)
	assert.Contains(t, string(out), "kit-lint-ok")
	assert.Contains(t, string(out), "kit-test-ok")
}

// TestHookGateResolution_KitTomlSingleQuotedField guards Fix 4: the
// embedded hook used to only match double-quoted values via sed. TOML
// allows single quotes as a literal string, and adopters routinely
// reach for them when the command itself contains double quotes.
func TestHookGateResolution_KitTomlSingleQuotedField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	// Use single quotes for both fields — valid TOML literal strings.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".kit", "pre-pr.toml"),
		[]byte("lint = 'echo single-lint-ok'\n"+
			"test = 'echo single-test-ok'\n"), 0o644))
	mustRun(t, root, "git", "add", ".kit/pre-pr.toml")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr,
		"single-quoted TOML values must resolve as gate commands:\n%s", out)
	assert.Contains(t, string(out), "single-lint-ok")
	assert.Contains(t, string(out), "single-test-ok")
}

// TestHookGateResolution_KitTomlTrailingComment also guards Fix 4: the
// sed-based parser must strip trailing whitespace and `#`-prefixed
// comments after the closing quote so adopters can annotate their
// gates.
func TestHookGateResolution_KitTomlTrailingComment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".kit", "pre-pr.toml"),
		[]byte("lint = \"echo cmnt-lint-ok\"  # run linter\n"+
			"test = \"echo cmnt-test-ok\"\t# run tests\n"), 0o644))
	mustRun(t, root, "git", "add", ".kit/pre-pr.toml")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr,
		"double-quoted TOML with trailing comment must resolve:\n%s", out)
	assert.Contains(t, string(out), "cmnt-lint-ok")
	assert.Contains(t, string(out), "cmnt-test-ok")
}

// TestHookScratchpadPath_WindowsShells guards Fix 5 Part A: the bash
// hook must recognise Git Bash / MSYS / Cygwin / MINGW environments
// (where `uname -s` returns MINGW64_NT-*, MSYS_NT-*, CYGWIN_NT-*) and
// derive the scratchpad base from $LOCALAPPDATA/Temp with the same
// fallback chain as the Go side: LOCALAPPDATA + /Temp, else TEMP
// (no extra /Temp), else TMPDIR, else /tmp.
//
// We override `uname` via a PATH-stubbed wrapper so the host kernel
// doesn't decide the outcome.
func TestHookScratchpadPath_WindowsShells(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}

	hookBytes, err := loadPrePrHookBytes()
	require.NoError(t, err)

	cases := []struct {
		name        string
		unameOutput string
		env         map[string]string
		// wantSuffix asserts the path ends with this; we don't pin the
		// exact base prefix because filepath separators on the bash
		// side stay forward-slash even when the env vars carry
		// backslashes (Git Bash translates).
		wantContains []string
	}{
		{
			name:        "MINGW64_NT with LOCALAPPDATA",
			unameOutput: "MINGW64_NT-10.0-19045",
			env: map[string]string{
				"LOCALAPPDATA": `C:\Users\foo\AppData\Local`,
			},
			wantContains: []string{"AppData", "Local", "Temp", "acme.scratchpad"},
		},
		{
			name:        "MSYS_NT with LOCALAPPDATA",
			unameOutput: "MSYS_NT-10.0-19045",
			env: map[string]string{
				"LOCALAPPDATA": `C:\Users\foo\AppData\Local`,
			},
			wantContains: []string{"AppData", "Local", "Temp", "acme.scratchpad"},
		},
		{
			name:        "CYGWIN_NT with LOCALAPPDATA",
			unameOutput: "CYGWIN_NT-10.0",
			env: map[string]string{
				"LOCALAPPDATA": `C:\Users\foo\AppData\Local`,
			},
			wantContains: []string{"AppData", "Local", "Temp", "acme.scratchpad"},
		},
		{
			name:        "MINGW64_NT without LOCALAPPDATA, with TEMP",
			unameOutput: "MINGW64_NT-10.0-19045",
			env: map[string]string{
				"TEMP": `C:\Users\foo\AppData\Local\Temp`,
			},
			// TEMP already includes Temp; must not double it.
			wantContains: []string{"AppData", "Local", "Temp", "acme.scratchpad"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpdir := t.TempDir()

			// uname shim.
			unameStub := filepath.Join(tmpdir, "uname")
			require.NoError(t, os.WriteFile(unameStub,
				[]byte("#!/usr/bin/env bash\nprintf '%s\\n' '"+c.unameOutput+"'\n"),
				0o755))

			// Hook source with a `scratchpad_path "acme"` call appended
			// so we get just the path on stdout.
			hookSrc := filepath.Join(tmpdir, "pre-pr-source.sh")
			driver := append([]byte{}, hookBytes...)
			// Strip the top-level run section by truncating before
			// `# --- run gates ---`. We only want the function defs.
			if idx := bytes.Index(driver, []byte("# --- run gates")); idx > 0 {
				driver = driver[:idx]
			}
			driver = append(driver, []byte("\nscratchpad_path acme\n")...)
			require.NoError(t, os.WriteFile(hookSrc, driver, 0o755))

			cmd := exec.Command("bash", hookSrc)
			// Build env: explicit, plus the uname-stub dir first on PATH.
			envv := []string{
				"PATH=" + tmpdir + string(os.PathListSeparator) + os.Getenv("PATH"),
			}
			for k, v := range c.env {
				envv = append(envv, k+"="+v)
			}
			cmd.Env = envv
			out, runErr := cmd.CombinedOutput()
			require.NoError(t, runErr, "hook scratchpad_path errored:\n%s", out)
			got := strings.TrimSpace(string(out))
			for _, want := range c.wantContains {
				assert.Contains(t, got, want,
					"scratchpad_path for %s must contain %q; got %q",
					c.unameOutput, want, got)
			}
			// Specifically guard against the doubled-Temp bug.
			assert.NotContains(t, got, "Temp/Temp",
				"scratchpad_path must not produce doubled Temp segment; got %q", got)
			assert.NotContains(t, got, `Temp\Temp`,
				"scratchpad_path must not produce doubled Temp segment; got %q", got)
		})
	}
}

func TestHookGateResolution_NoGateDeclared(t *testing.T) {
	// No Makefile, no mise, no .kit/pre-pr.toml → single-line stderr
	// note per gate, exit 0.
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "no-gate run must exit 0; output:\n%s", out)
	assert.Contains(t, string(out), "no lint gate declared")
	assert.Contains(t, string(out), "no test gate declared")
}

func TestHookScratchpadGate_FailsOnTrackedMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	// Track a file containing a scratchpad marker.
	bad := []byte("package x\n\n// SCRATCH: refactor later\nfunc f() {}\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "x.go"), bad, 0o644))
	mustRun(t, root, "git", "add", "x.go")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.Error(t, runErr, "scratchpad gate must fail; output:\n%s", out)

	ee, ok := runErr.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 3, ee.ExitCode(), "scratchpad gate exit code must be 3")
	assert.Contains(t, string(out), "scratchpad gate failed")
	assert.Contains(t, string(out), "x.go")
}

func TestHookLintGate_FailureBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook is bash-only on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "t@e")
	mustRun(t, root, "git", "config", "user.name", "t")

	require.NoError(t, os.WriteFile(filepath.Join(root, "Makefile"), []byte(
		"lint:\n\t@echo lint-fail; exit 7\n"+
			"test:\n\t@echo test-ok\n"),
		0o644))
	mustRun(t, root, "git", "add", "Makefile")
	mustRun(t, root, "git", "commit", "-q", "-m", "init")

	_, err := GeneratePrePrHook(root, false, fixedTime())
	require.NoError(t, err)

	hook := filepath.Join(root, PrePrHookPath)
	require.NoError(t, os.Chmod(hook, 0o755))
	cmd := exec.Command("bash", hook)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	require.Error(t, runErr)

	ee, ok := runErr.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 1, ee.ExitCode(), "lint failure exit code must be 1")
	assert.Contains(t, string(out), "pre-pr lint failed")
	// Test gate should not have run after lint failure.
	assert.NotContains(t, string(out), "test-ok")
}

// --- helpers -----------------------------------------------------------

// mapEnv returns a function suitable for ScratchpadPath's env arg backed
// by the given map. Keys absent from the map return "".
func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// mustRun invokes the given command in dir and fails the test if it
// exits non-zero. Output is folded into the failure message.
func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// Compile-time guard: ensure the embedded asset is non-empty so a sync
// regression surfaces as a build failure rather than a runtime no-op.
var _ = func() bool {
	b, err := loadPrePrHookBytes()
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		panic("prepr_assets/pre-pr.sh is empty or missing from embed")
	}
	return true
}()
