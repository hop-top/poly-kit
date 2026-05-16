package grade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCITemplatesPresent regression-guards the four template files +
// README. CI workflows depend on the canonical paths; this test
// fails loudly on rename.
func TestCITemplatesPresent(t *testing.T) {
	repoRoot := findRepoRoot(t)
	want := []string{
		"templates/ci/grade/github-actions.yml",
		"templates/ci/grade/gitlab-ci.yml",
		"templates/ci/grade/buildkite.yml",
		"templates/ci/grade/generic.sh",
		"templates/ci/grade/README.md",
	}
	for _, rel := range want {
		path := filepath.Join(repoRoot, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing template %s: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("empty template %s", rel)
		}
	}
}

// TestGitHubActionsSHAsPinned asserts the workflow uses SHA-pinned
// actions (no @v4-style tag pins) — kit's verify-no-leak preferred
// hardening. Regression test for design.md §9.
func TestGitHubActionsSHAsPinned(t *testing.T) {
	repoRoot := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot, "templates/ci/grade/github-actions.yml"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	body := string(raw)
	for _, action := range []string{
		"actions/checkout@",
		"actions/setup-go@",
		"actions/upload-artifact@",
	} {
		idx := strings.Index(body, action)
		if idx < 0 {
			t.Errorf("template missing %s", action)
			continue
		}
		// Expect 40 hex chars after the @.
		rest := body[idx+len(action):]
		if len(rest) < 40 {
			t.Errorf("%s pin too short", action)
			continue
		}
		for i := 0; i < 40; i++ {
			c := rest[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("%s not SHA-pinned (saw %q)", action, rest[:min(40, len(rest))])
				break
			}
		}
	}
}

// TestGitHubActionsEnablesPostingByDefault asserts the github
// template ships --pr-comment + --status-check pre-enabled per
// design.md §9 ("github template enables PR-comment + status-check
// by default; other providers ship minimal shapes").
func TestGitHubActionsEnablesPostingByDefault(t *testing.T) {
	repoRoot := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot, "templates/ci/grade/github-actions.yml"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "--pr-comment") {
		t.Error("github-actions template does not enable --pr-comment")
	}
	if !strings.Contains(body, "--status-check") {
		t.Error("github-actions template does not enable --status-check")
	}
}

// findRepoRoot walks up from the test's CWD until it finds a go.mod
// or a Makefile. Tests need the repo root to find templates that
// live outside the test package's dir.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod up from cwd)")
		}
		dir = parent
	}
}
