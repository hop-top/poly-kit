package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestIsKitInternal_PolyKitRename covers the substring-match bug that
// caused the kit-internal allowlist to NOT load on hop-top/poly-kit
// (repo was renamed from hop-top/kit). Without this, the daily
// verify-no-leak-audit ran without the allowlist and flagged scenario
// testdata files as findings.
func TestIsKitInternal_PolyKitRename(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{"hop-top/kit SSH", "git@github.com:hop-top/kit.git\n", true},
		{"hop-top/kit HTTPS", "https://github.com/hop-top/kit.git\n", true},
		{"hop-top/kit no .git", "https://github.com/hop-top/kit\n", true},
		{"hop-top/poly-kit SSH", "git@github.com:hop-top/poly-kit.git\n", true},
		{"hop-top/poly-kit HTTPS", "https://github.com/hop-top/poly-kit.git\n", true},
		{"hop-top/poly-kit no newline", "https://github.com/hop-top/poly-kit", true},
		{"hop-top/kit-fork rejected", "https://github.com/hop-top/kit-fork.git\n", false},
		{"adopter repo", "https://github.com/acme/our-cli.git\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := setupFakeGitRemote(t, tc.url)
			t.Setenv("KIT_INTERNAL_ALLOWLIST", "")
			got := isKitInternal(cwd)
			if got != tc.want {
				t.Errorf("isKitInternal(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestIsKitInternal_EnvOverride(t *testing.T) {
	cwd := setupFakeGitRemote(t, "https://github.com/acme/unrelated.git\n")
	t.Setenv("KIT_INTERNAL_ALLOWLIST", "1")
	if !isKitInternal(cwd) {
		t.Error("env override should force-allowlist")
	}
}

// setupFakeGitRemote creates a temp dir with a git repo whose
// remote.origin.url is set to url. Uses set-url with a fallback to add
// so dev templates that pre-create origin don't conflict.
func setupFakeGitRemote(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TEMPLATE_DIR=")
	runGit := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	if out, err := runGit("init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Idempotent: add if missing, set-url if already present.
	if out, err := runGit("remote", "add", "origin", url); err != nil {
		if out2, err2 := runGit("remote", "set-url", "origin", url); err2 != nil {
			t.Fatalf("git remote add: %v\n%s\nset-url fallback: %v\n%s", err, out, err2, out2)
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
