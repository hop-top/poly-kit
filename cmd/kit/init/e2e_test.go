//go:build e2e

// Package kitinit_test — e2e tests gated by the `e2e` build tag.
//
// These tests build the kit binary, invoke `kit init` as a subprocess,
// and assert observable side-effects on disk + stdout. They are excluded
// from the default `go test ./...` run; enable via:
//
//	go test -tags=e2e ./cmd/kit/init/ -run TestE2E -v
//
// Until T-0867 wires `initCmd` into cmd/kit/main.go, the kit binary will
// not have an `init` subcommand. Each test probes `kit init --help` and
// skips with a clear message when the subcommand is unregistered. Once
// T-0867 lands, the same suite begins exercising the real flow with no
// edits required.
package kitinit_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildKitBinary compiles cmd/kit into a temp file and returns its path.
// Mirrors cmd/kit/serve_test.go::buildBinary so behaviour stays consistent.
func buildKitBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kit-e2e")
	cmd := exec.Command("go", "build", "-mod=mod", "-buildvcs=false", "-o", bin, "./cmd/kit/")
	// cwd: cmd/kit/init/ → repo root is two levels up.
	cmd.Dir = filepath.Join("..", "..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %s\n%s", err, out)
	}
	return bin
}

// hasInitSubcommand returns true when `init` appears as a registered
// subcommand in the root help output; the kit CLI framework swallows
// unknown subcommands when --help is passed (renders root help with
// exit 0), so a flag-level probe is unreliable. Parsing the root help
// listing is the most robust check.
func hasInitSubcommand(t *testing.T, bin string) bool {
	t.Helper()
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		return false
	}
	// Match a leading "init " or "init [" entry in the COMMANDS block —
	// avoid false positives on words like "initialize" or "--init-foo".
	for _, line := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "init ") || strings.HasPrefix(s, "init\t") || s == "init" {
			return true
		}
	}
	return false
}

// skipUnlessInitWired returns the kit binary path or skips the test when
// `kit init` is not yet registered (pre-T-0867).
func skipUnlessInitWired(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := buildKitBinary(t)
	if !hasInitSubcommand(t, bin) {
		t.Skip("kit init subcommand not registered — pending T-0867")
	}
	return bin
}

// runKit runs the kit binary with args from cwd and returns combined output.
//
// Test isolation pattern: HOME points at a fresh tmpdir so the host's git
// config / signing keys / template registry can't leak in. We then seed a
// minimal `.gitconfig` in that HOME so `git config --get user.{name,email}`
// resolves cleanly during inputs.Gather (cli-go manifest declares Author +
// Email as required vars; built-in fallback shells out to `git config`).
// GIT_AUTHOR_*/GIT_COMMITTER_* env vars cover the commit identity.
func runKit(t *testing.T, bin, cwd string, args ...string) ([]byte, error) {
	t.Helper()
	home := t.TempDir()
	gitconfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Test User\n\temail = test@example.com\n"), 0o644); err != nil {
		t.Fatalf("seed .gitconfig: %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	return cmd.CombinedOutput()
}

func TestE2E_Bootstrap_Personal_Go(t *testing.T) {
	bin := skipUnlessInitWired(t)
	work := t.TempDir()

	out, err := runKit(t, bin, work,
		"init", "mytool",
		"--from=cli-go",
		"--account-type=personal",
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	)
	if err != nil {
		t.Fatalf("kit init failed: %v\n%s", err, out)
	}

	target := filepath.Join(work, "mytool")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("project dir not created: %v", err)
	}
	// Tier-4 default → Makefile + go.mod + main.go all expected.
	for _, rel := range []string{"go.mod", "Makefile", ".kit/version"} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Errorf("expected %s in scaffolded project: %v", rel, err)
		}
	}
}

func TestE2E_Bootstrap_Org_MultiRuntime(t *testing.T) {
	bin := skipUnlessInitWired(t)
	work := t.TempDir()

	// `multi-runtime` template not in builtins (cli-go/cli-py/cli-ts only);
	// exercise the org+visibility path with cli-go and assert org-scoped
	// outputs land on disk.
	out, err := runKit(t, bin, work,
		"init", "mytool",
		"--account-type=org",
		"--org=acme",
		"--from=cli-go",
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	)
	if err != nil {
		t.Fatalf("kit init failed: %v\n%s", err, out)
	}
	target := filepath.Join(work, "mytool")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("project dir not created: %v", err)
	}
	gomod, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	// Module derives from org when --account-type=org → github.com/acme/mytool.
	if !strings.Contains(string(gomod), "acme/mytool") {
		t.Errorf("expected go.mod module to include acme/mytool, got: %s", gomod)
	}
}

func TestE2E_Augment_Tier1(t *testing.T) {
	bin := skipUnlessInitWired(t)
	work := t.TempDir()

	// augment requires existing git repo; seed one with an existing file
	// so detection picks "augment" mode.
	gitInit := exec.Command("git", "init", work)
	gitInit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
	)
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, "existing.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// `work` is a t.TempDir() — base name like "TestE2E_Augment_Tier1123" fails
	// the manifest Name regex (^[a-z][a-z0-9-]{0,63}$). Pass the project name
	// as positional arg instead of leaning on augment.go's filepath.Base(cwd)
	// fallback. The init command treats args[0] as the project name regardless
	// of bootstrap/augment mode.
	out, err := runKit(t, bin, work,
		"init", "mytool",
		"--tier=1",
		"--from=cli-go",
		"--no-github",
		"--yes",
	)
	if err != nil {
		t.Fatalf("kit init augment failed: %v\n%s", err, out)
	}

	// Tier 1 contract: lint/build only (.golangci.yml, .gitignore, Makefile,
	// go.mod, .trivyignore). main.go is tier-3 — must NOT be present.
	for _, rel := range []string{"Makefile", ".golangci.yml"} {
		if _, err := os.Stat(filepath.Join(work, rel)); err != nil {
			t.Errorf("expected tier-1 file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "main.go")); err == nil {
		t.Errorf("main.go (tier 3) should not exist at tier 1")
	}
}

func TestE2E_DryRun_NoWrites(t *testing.T) {
	bin := skipUnlessInitWired(t)
	work := t.TempDir()

	out, err := runKit(t, bin, work,
		"init", "mytool",
		"--from=cli-go",
		"--dry-run",
		"--no-github",
		"--no-push",
		"--yes",
	)
	if err != nil {
		t.Fatalf("kit init --dry-run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "mytool")); !os.IsNotExist(err) {
		t.Errorf("expected mytool dir to NOT exist under --dry-run, stat err: %v", err)
	}
}

func TestE2E_JSON_Parses(t *testing.T) {
	bin := skipUnlessInitWired(t)
	work := t.TempDir()

	// JSON-summary toggle now reads from the kit-owned `--format` global
	// (parity contract §3.3); the deprecated init-local --json flag was
	// removed in favor of `--format json`.
	out, err := runKit(t, bin, work,
		"--format", "json",
		"init", "mytool",
		"--from=cli-go",
		"--account-type=personal",
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	)
	if err != nil {
		t.Fatalf("kit init --format json failed: %v\n%s", err, out)
	}
	// Stdout may include log lines before the JSON document; locate the
	// first '{' and decode from there.
	idx := bytes.IndexByte(out, '{')
	if idx < 0 {
		t.Fatalf("no JSON object in stdout: %s", out)
	}
	var summary map[string]any
	if err := json.Unmarshal(out[idx:], &summary); err != nil {
		t.Fatalf("unmarshal summary: %v\nraw: %s", err, out[idx:])
	}
	if summary["name"] != "mytool" {
		t.Errorf("expected summary.name=mytool, got %v", summary["name"])
	}
	if summary["mode"] != "bootstrap" {
		t.Errorf("expected summary.mode=bootstrap, got %v", summary["mode"])
	}
}
