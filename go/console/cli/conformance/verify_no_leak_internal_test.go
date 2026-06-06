package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			scrubGitEnvForTest(t)
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
	scrubGitEnvForTest(t)
	cwd := setupFakeGitRemote(t, "https://github.com/acme/unrelated.git\n")
	t.Setenv("KIT_INTERNAL_ALLOWLIST", "1")
	if !isKitInternal(cwd) {
		t.Error("env override should force-allowlist")
	}
}

// scrubGitEnvForTest unsets GIT_* vars in the test process so
// isKitInternal's exec.Command("git", ...) doesn't resolve to the outer
// repo via inherited GIT_DIR (set when tests run under a pre-push hook).
// Empty-string values aren't safe — git rejects an empty GIT_DIR — so
// we Unsetenv and restore the originals in cleanup.
func scrubGitEnvForTest(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 || !strings.HasPrefix(kv, "GIT_") {
			continue
		}
		key, val := kv[:i], kv[i+1:]
		os.Unsetenv(key)
		t.Cleanup(func() { os.Setenv(key, val) })
	}
}

// setupFakeGitRemote creates a temp dir with a git repo whose
// remote.origin.url is set to url. Scrubs inherited GIT_* env vars so
// git operations don't resolve back to the outer repo via GIT_DIR —
// without this, running under a git hook (which sets GIT_DIR to the
// outer repo's git dir) would write to the outer repo's config.
func setupFakeGitRemote(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	// Build a clean env: keep PATH + HOME but strip every GIT_* var.
	env := []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TEMPLATE_DIR=",
	}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	runGit := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	if out, err := runGit("init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := runGit("remote", "add", "origin", url); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
