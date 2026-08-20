// White-box tests for the T-0981/T-0982/T-0983 scaffold-cluster fixes:
// shared-template composition (tier table, output mapping, skip
// reasons), bare-worktree detection, and the non-interactive git-hop
// fallback.
package kitinit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tmpl "hop.top/kit/internal/template"
)

func sharedTestInputs(t *testing.T) Inputs {
	t.Helper()
	return Inputs{
		Name:        "demo",
		Template:    "cli-go",
		Runtime:     []string{"go"},
		Tier:        4,
		License:     "MIT",
		AccountType: "none",
		Vars: map[string]any{
			"name": "demo", "Name": "demo",
			"module": "example.com/demo", "Module": "example.com/demo",
			"description": "demo project", "Description": "demo project",
			"License":    "MIT",
			"Year":       2026,
			"Author":     "Jane Doe",
			"Copyrights": DefaultCopyrights(2026),
			"Email":      "jane@example.com",
			"Org":        "",
			"Runtime":    []string{"go"},
			"tier":       4,
		},
	}
}

func sharedDeps(t *testing.T) Deps {
	t.Helper()
	cache := t.TempDir()
	return Deps{
		Registry: tmpl.NewRegistry("", cache),
		Hooks:    &recordingHookRunner{},
		Git:      &recordingGitRunner{},
		GitHub:   &recordingGitHubRunner{},
		Output:   os.Stderr,
	}
}

// TestRenderShared_Tier0Bootstrap asserts the full shared composition
// lands where scaffold.sh would put it (T-0983).
func TestRenderShared_Tier0Bootstrap(t *testing.T) {
	target := t.TempDir()
	in := sharedTestInputs(t)
	sum, err := renderShared(context.Background(), sharedDeps(t), in, target, 0, false)
	if err != nil {
		t.Fatalf("renderShared: %v", err)
	}
	for _, want := range []string{
		".gitignore",
		".gitattributes",
		".github/workflows/ci-go.yml",
		".github/dependabot.yml",
		"LICENSE",
		"CONTRIBUTING.md",
		"SECURITY.md",
		"RELEASING.md",
		"scripts/promote-release.sh",
	} {
		if _, statErr := os.Stat(filepath.Join(target, want)); statErr != nil {
			t.Errorf("expected %s to exist: %v", want, statErr)
		}
	}
	// Composition content checks.
	gi, _ := os.ReadFile(filepath.Join(target, ".gitignore"))
	if !strings.Contains(string(gi), ">>> kit-managed: gitignore >>>") {
		t.Errorf(".gitignore missing managed markers:\n%s", gi)
	}
	lic, _ := os.ReadFile(filepath.Join(target, "LICENSE"))
	if !strings.Contains(string(lic), "MIT License") {
		t.Errorf("LICENSE not rendered from MIT source:\n%.120s", lic)
	}
	if !strings.Contains(string(lic), "Jane Doe") && !strings.Contains(string(lic), "Idea Crafters") {
		t.Errorf("LICENSE missing copyright holders:\n%.240s", lic)
	}
	ci, _ := os.ReadFile(filepath.Join(target, ".github", "workflows", "ci-go.yml"))
	if !strings.Contains(string(ci), "env CGO_ENABLED=1 go test -race") {
		t.Errorf("ci-go.yml missing mise-exec env cgo override")
	}
	if !strings.Contains(string(ci), ".github/workflows/ci-go.yml") {
		t.Errorf("ci-go.yml paths filter must include the workflow itself")
	}
	// Every skip carries a named reason; the emitter machinery is
	// excluded at the manifest level and non-selected runtimes at the
	// mapping level.
	var sawRuntimeSkip, sawExcludeSkip bool
	for _, sk := range sum.Skipped {
		if sk.Reason == "runtime-not-selected" {
			sawRuntimeSkip = true
		}
		if sk.Reason == "exclude-rule" {
			sawExcludeSkip = true
		}
		if sk.Reason == "" {
			t.Errorf("skip without reason: %s", sk.Path)
		}
	}
	if !sawRuntimeSkip {
		t.Error("expected runtime-not-selected skips (ci-ts.yml etc.)")
	}
	if !sawExcludeSkip {
		t.Error("expected exclude-rule skips (emitter machinery)")
	}
	// JSON round-trip: reasons must survive serialization.
	b, err := json.Marshal(sum)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), "exclude-rule") {
		t.Error("SharedSummary JSON must name skip reasons")
	}
}

// TestRenderShared_TierTable exercises the tier gate: tier 1 gets
// gitignore/gitattributes but no CI, LICENSE, or docs; tier 2 adds CI +
// LICENSE + release script; tier 4 adds contribution docs.
func TestRenderShared_TierTable(t *testing.T) {
	cases := []struct {
		tier    int
		present []string
		absent  []string
	}{
		{1,
			[]string{".gitignore", ".gitattributes"},
			[]string{".github/workflows/ci-go.yml", "LICENSE", "CONTRIBUTING.md", "scripts/promote-release.sh"}},
		{2,
			[]string{".gitignore", ".github/workflows/ci-go.yml", "LICENSE", "scripts/promote-release.sh"},
			[]string{"CONTRIBUTING.md", "SECURITY.md"}},
		{4,
			[]string{".gitignore", ".github/workflows/ci-go.yml", "LICENSE", "CONTRIBUTING.md", "SECURITY.md", "RELEASING.md"},
			nil},
	}
	for _, tc := range cases {
		target := t.TempDir()
		in := sharedTestInputs(t)
		in.Tier = tc.tier
		if _, err := renderShared(context.Background(), sharedDeps(t), in, target, tc.tier, false); err != nil {
			t.Fatalf("tier %d: %v", tc.tier, err)
		}
		for _, p := range tc.present {
			if _, err := os.Stat(filepath.Join(target, p)); err != nil {
				t.Errorf("tier %d: want %s present: %v", tc.tier, p, err)
			}
		}
		for _, p := range tc.absent {
			if _, err := os.Stat(filepath.Join(target, p)); err == nil {
				t.Errorf("tier %d: want %s ABSENT", tc.tier, p)
			}
		}
	}
}

// TestRenderShared_NonDestructive: existing differing file becomes a
// .kit-suggested sibling; identical file is skipped as such.
func TestRenderShared_NonDestructive(t *testing.T) {
	target := t.TempDir()
	in := sharedTestInputs(t)
	// Pre-seed a user CONTRIBUTING.md that differs.
	if err := os.WriteFile(filepath.Join(target, "CONTRIBUTING.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := renderShared(context.Background(), sharedDeps(t), in, target, 4, false)
	if err != nil {
		t.Fatalf("renderShared: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, "CONTRIBUTING.md")); string(got) != "mine\n" {
		t.Error("existing CONTRIBUTING.md must not be overwritten")
	}
	if _, err := os.Stat(filepath.Join(target, ".kit-suggested.CONTRIBUTING.md")); err != nil {
		t.Errorf("expected .kit-suggested.CONTRIBUTING.md sibling: %v", err)
	}
	found := false
	for _, s := range sum.Suggested {
		if strings.Contains(s, "CONTRIBUTING.md") {
			found = true
		}
	}
	if !found {
		t.Error("summary must report the suggested sibling")
	}
	// Second run over the now-composed tree: .gitignore identical → skip.
	sum2, err := renderShared(context.Background(), sharedDeps(t), in, target, 4, false)
	if err != nil {
		t.Fatalf("renderShared #2: %v", err)
	}
	identical := false
	for _, sk := range sum2.Skipped {
		if sk.Path == ".gitignore" && sk.Reason == "identical" {
			identical = true
		}
	}
	if !identical {
		t.Error("re-run must skip identical .gitignore")
	}
}

// TestDetect_WorktreeOfBare: a linked worktree of a bare repo must flow
// to augment (T-0981), while the bare root stays refused.
func TestDetect_WorktreeOfBare(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "hub")
	seed := filepath.Join(root, "seed")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(root, "init", "-b", "main", seed)
	run(seed, "commit", "--allow-empty", "-m", "seed")
	run(root, "clone", "--bare", seed, bare)
	wt := filepath.Join(root, "wt")
	run(bare, "worktree", "add", wt, "main")

	mode, _, err := Detect(wt, ModeUnset)
	if err != nil {
		t.Fatalf("detect worktree: %v", err)
	}
	if mode != ModeAugment {
		t.Errorf("worktree of bare: want augment, got %s", mode)
	}
	mode, _, err = Detect(bare, ModeUnset)
	if err != nil {
		t.Fatalf("detect bare root: %v", err)
	}
	if mode != ModeBareWorktree {
		t.Errorf("bare ROOT: want bare_worktree refusal, got %s", mode)
	}
}

// TestInit_NonInteractiveFallback: a git-hop that demands a TTY under
// --yes falls back to plain git init instead of failing (T-0982).
func TestInit_NonInteractiveFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	// Fake git-hop: LookPath("git-hop") must succeed, and `git hop ...`
	// resolves through git's exec path (git-hop on PATH). The fake
	// prints the cannot-prompt failure and exits 129.
	shim := "#!/bin/sh\necho 'fatal: cannot prompt for confirmation on a non-interactive stdin; pass --no-prompt to proceed without confirmation' >&2\nexit 129\n"
	if err := os.WriteFile(filepath.Join(bin, "git-hop"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Dir(realGit))

	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	outcome, err := Init(context.Background(), dir, true, "main", true)
	if err != nil {
		t.Fatalf("Init must fall back, got error: %v", err)
	}
	if !outcome.FellBack {
		t.Error("expected FellBack=true")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("fallback must leave a plain git repo: %v", err)
	}
}
