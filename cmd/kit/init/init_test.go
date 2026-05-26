// Integration tests for InitCmd's full RunE flow (T-0862).
//
// Drives detect → Gather → bootstrap/augment dispatch → output through the
// cobra command rather than runBootstrap/runAugment directly. White-box
// (package kitinit) for access to unexported sentinels (IsAlreadyKit,
// IsOrgRequired) and to keep parity with bootstrap_test / augment_test.
//
// Strategy: avoid the built-in cli-go template (T-0954 PascalCase manifest
// var lookup bug breaks `kit init --from=cli-go` end-to-end) by writing a
// local fixture template per test. Once T-0954 lands, switching tests to
// `--from=cli-go` is a one-line change.
//
// Real git on PATH is required for bootstrap success paths (init + initial
// commit). Tests skip cleanly when git is absent. GitHub is bypassed via
// --no-github so no stub-gh needed; bootstrap also receives --no-push.
package kitinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// fixtureInitTemplate writes a small synthetic template with lowercase
// manifest variables (avoids the T-0954 PascalCase lookup bug). Returns
// the absolute path so InitCmd's --from flag can resolve it via Registry.
// File set:
//
//	tier 1: lint.yml, Makefile, .gitignore
//	tier 3: + main.go (uses {{.name}} so we exercise template rendering)
//	tier 4: + README.md
func fixtureInitTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	manifest := `name: fixture
description: "synthetic template for init_test"
kit_version: ">=0.4.0"
variables:
  - name: name
    prompt: "name"
files:
  exclude:
    - "kit-template.yaml"
    - "tiers.yaml"
render_rules:
  strip_suffixes: [".tmpl"]
hooks: {}
`
	tiers := `files:
  "lint.yml": [1, 2, 3, 4]
  "Makefile": [1, 2, 3, 4]
  ".gitignore": [1, 2, 3, 4]
  "go.mod": [1, 2, 3, 4]
  "main.go": [3, 4]
  "README.md": [4]
`
	files := map[string]string{
		"kit-template.yaml": manifest,
		"tiers.yaml":        tiers,
		"lint.yml":          "rules: []\n",
		"Makefile":          "build:\n\t@echo build\n",
		".gitignore":        "/bin\n",
		"go.mod.tmpl":       "module github.com/example/{{.name}}\n\ngo 1.22\n",
		"main.go.tmpl":      "package main // {{.name}}\n",
		"README.md.tmpl":    "# {{.name}}\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o640))
	}
	return dir
}

// skipIfNoGit short-circuits tests that need a real git binary (bootstrap
// performs git init + initial commit through the production GitRunner).
func skipIfNoGitInit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// configureGitInWorkdir sets a per-test git identity so initial-commit
// works on hosts without a global git config. Applied after `git init`
// (post-bootstrap) by walking down to the created project dir.
func configureGitInWorkdir(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %v: %v\n%s", args, err, out)
		}
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// what was written. Needed because InitCmd writes summary directly to
// os.Stdout (not cmd.OutOrStdout), so cobra's SetOut doesn't help.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-done
}

// preCommitGitIdentity seeds a global-ish git identity before `git init`
// runs inside bootstrap. We use GIT_AUTHOR_*/GIT_COMMITTER_* env vars
// (honored by git regardless of repo-local config) so the initial commit
// inside runBootstrap succeeds without us being able to chdir into the
// not-yet-created project dir to set repo-local config first.
func preCommitGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	// Avoid host's git config (e.g. signing keys) interfering.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestInit_Bootstrap_HappyPath(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"mytool",
		"--from=" + tplPath,
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	require.NoError(t, cmd.Execute())

	target := filepath.Join(work, "mytool")
	require.DirExists(t, target)
	for _, rel := range []string{"go.mod", "main.go", ".kit/version"} {
		assert.FileExists(t, filepath.Join(target, rel))
	}
	// T-0773: pre-pr hook + manifest scaffolded under default flags.
	assert.FileExists(t, filepath.Join(target, ".githooks/pre-pr"))
	assert.FileExists(t, filepath.Join(target, ".kit/generated.json"))

	// .kit/version content matches manifest name + "@latest".
	got, err := os.ReadFile(filepath.Join(target, ".kit", "version"))
	require.NoError(t, err)
	assert.Equal(t, "fixture@latest\n", string(got))
}

func TestInit_Bootstrap_WithoutPrePrHook_Skips(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"mytool",
		"--from=" + tplPath,
		"--hop=false",
		"--no-github",
		"--no-push",
		"--without-githook-pre-pr",
		"--without-github-workflows",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	require.NoError(t, cmd.Execute())

	target := filepath.Join(work, "mytool")
	require.DirExists(t, target)

	// Hook and manifest absent under --without-githook-pre-pr. Workflows
	// share the same .kit/generated.json manifest file, so both
	// generators must be disabled to keep the manifest off-disk.
	_, hErr := os.Stat(filepath.Join(target, ".githooks/pre-pr"))
	assert.True(t, os.IsNotExist(hErr),
		"--without-githook-pre-pr must skip hook; stat err=%v", hErr)
	_, mErr := os.Stat(filepath.Join(target, ".kit/generated.json"))
	assert.True(t, os.IsNotExist(mErr),
		"--without-githook-pre-pr must skip manifest; stat err=%v", mErr)
}

func TestInit_Bootstrap_DryRun(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"mytool",
		"--from=" + tplPath,
		"--dry-run",
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	require.NoError(t, cmd.Execute())

	// Dry run skips mkdir entirely.
	_, err := os.Stat(filepath.Join(work, "mytool"))
	assert.True(t, os.IsNotExist(err),
		"expected mytool dir NOT to exist under --dry-run; stat err=%v", err)
}

func TestInit_Bootstrap_JSON(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	t.Chdir(work)

	// JSON-summary toggle now reads from the kit-owned `--format` global
	// (parity contract §3.3); the deprecated init-local --json flag was
	// removed. Inject a viper with format=json via a minimal cli.Root so
	// InitCmd picks up the same value the wired CLI would.
	vp := viper.New()
	vp.Set("format", "json")
	out := captureStdout(t, func() {
		cmd := InitCmd(&cli.Root{Viper: vp})
		cmd.SetArgs([]string{
			"mytool",
			"--from=" + tplPath,
			"--hop=false",
			"--no-github",
			"--no-push",
			"--yes",
		})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		require.NoError(t, cmd.Execute())
	})

	idx := bytes.IndexByte(out, '{')
	require.GreaterOrEqual(t, idx, 0, "no JSON object in stdout: %s", out)

	var summary map[string]any
	require.NoError(t, json.Unmarshal(out[idx:], &summary),
		"unmarshal failed; raw: %s", out[idx:])
	assert.Equal(t, "bootstrap", summary["mode"])
	assert.Equal(t, "mytool", summary["name"])
}

func TestInit_Augment_AddsFiles(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	// Pre-create .git so detect picks ModeAugment.
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".git"), 0o750))
	// Pre-existing custom README — engine must route the rendered
	// template to .kit-suggested.README.md and leave this untouched.
	customReadme := []byte("# user-curated readme\n")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), customReadme, 0o640))
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"--from=" + tplPath,
		"--tier=4",
		"--no-github",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	require.NoError(t, cmd.Execute())

	// Tier-4 marker file written.
	assert.FileExists(t, filepath.Join(work, "Makefile"))
	assert.FileExists(t, filepath.Join(work, "main.go"))

	// Original README untouched.
	got, err := os.ReadFile(filepath.Join(work, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, string(customReadme), string(got),
		"existing README.md must remain unchanged")

	// Suggested sibling materialized with rendered content.
	assert.FileExists(t, filepath.Join(work, "README.md.kit-suggested"))

	// .kit/version written by augment too.
	assert.FileExists(t, filepath.Join(work, ".kit", "version"))
}

func TestInit_Augment_Tier1_OnlyMinimal(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".git"), 0o750))
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"--from=" + tplPath,
		"--tier=1",
		"--no-github",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	require.NoError(t, cmd.Execute())

	// Tier-1 files present.
	for _, rel := range []string{"Makefile", "lint.yml", ".gitignore", "go.mod"} {
		assert.FileExists(t, filepath.Join(work, rel))
	}
	// Tier-3 / tier-4 files absent.
	for _, rel := range []string{"main.go", "README.md"} {
		_, err := os.Stat(filepath.Join(work, rel))
		assert.True(t, os.IsNotExist(err),
			"tier-1 must not write %s; stat err=%v", rel, err)
	}
}

func TestInit_Augment_AlreadyKit(t *testing.T) {
	work := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".git"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".kit"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(work, ".kit", "version"),
		[]byte("fixture@1.2.3\n"),
		0o644,
	))
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{"--no-github", "--yes"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.True(t, IsAlreadyKit(err),
		"expected ErrAlreadyKit; got %v", err)
}

// TestInit_NilRoot_DoesNotPanic is the regression for T-0229..T-0231.
// InitCmd is documented as accepting a nil *cli.Root; prior to the fix,
// RunE deref'd root.Viper unconditionally and SIGSEGV'd. Drive the
// command end-to-end with nil root through a benign code path
// (ModeAlreadyKit short-circuits before any heavy lifting) and assert
// no panic and a sensible typed error.
func TestInit_NilRoot_DoesNotPanic(t *testing.T) {
	work := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".git"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".kit"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(work, ".kit", "version"),
		[]byte("fixture@1.2.3\n"),
		0o644,
	))
	t.Chdir(work)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InitCmd(nil).Execute panicked: %v", r)
		}
	}()

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{"--no-github", "--yes"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err, "expected ModeAlreadyKit error, got nil")
	assert.True(t, IsAlreadyKit(err),
		"expected ErrAlreadyKit; got %v", err)
}

// TestInitCmd_ManagedFlagsRoute locks the cobra flag → RunManaged
// dispatch wiring at init.go's top-of-RunE branch. Behavior coverage
// for the managed orchestrator lives in managed_test.go; this test
// only verifies that the cobra-level flag check actually routes to
// RunManaged. A future rename like addServiceFlag → serviceFlag that
// forgets to update the branch condition would silently fall through
// to the bootstrap/augment path; here we catch that as an observable
// side-effect (no mise.toml emitted).
//
// Strategy: drive a minimal Go project (just `go.mod`) through
// `kit init --update --yes` and assert the managed-block scaffold
// landed. We use --update (the cheapest managed flag) and the
// presence of mise.toml as the proxy for "RunManaged ran".
func TestInitCmd_ManagedFlagsRoute(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH; managed-block dispatch requires bash")
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "go.mod"),
		[]byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"--update",
		"--langs=go",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	require.NoError(t, cmd.Execute())

	// Observable: RunManaged dropped a mise.toml. If the flag check
	// regressed and we fell through to bootstrap/augment, neither
	// path emits this file under these args (bootstrap needs a
	// positional name; augment needs .git/), so absence is a hard
	// signal.
	assert.FileExists(t, filepath.Join(work, "mise.toml"))
}

func TestInit_Bootstrap_ScopeError_OrgNoOrg(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"mytool",
		"--account-type=org",
		// --org deliberately omitted
		"--hop=false",
		"--no-github",
		"--no-push",
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOrgRequired),
		"expected ErrOrgRequired; got %v", err)
}
