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
