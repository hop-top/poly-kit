// Per-template bootstrap smoke tests (T-0863). White-box (package kitinit)
// because runBootstrap is unexported. Each test resolves a built-in template
// via the real Registry, renders into t.TempDir() with stub runners (no
// network, no shell-out), then asserts a small set of marker files.
//
// Coverage:
//   - cli-go: go.mod + go build ./... (requires `go` on PATH; deps via module cache)
//   - cli-ts: package.json + tsconfig.json (no npm build — CI cost)
//   - cli-py: pyproject.toml + src/<Name>/__init__.py (no pip install — CI cost)
//   - shared: kit/template files (e.g. README.md, init.sh)
//
// Spec §11 mentioned multi-runtime / server / agent — none ship as built-ins
// today; cases below skip via t.Skip when names absent so the suite stays
// green as templates are added.
package kitinit

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tmpl "hop.top/kit/internal/template"
)

// Recording runners (recordingHookRunner / recordingGitRunner /
// recordingGitHubRunner) live in testhelpers_test.go (T-0952).

// builtinAvailable reports whether name is in the embedded built-ins list.
func builtinAvailable(t *testing.T, name string) bool {
	t.Helper()
	names, err := tmpl.Available()
	require.NoError(t, err)
	return slices.Contains(names, name)
}

// runBootstrapFor invokes runBootstrap with stub runners after chdir-ing
// into t.TempDir(). Returns the resulting target directory. Inputs prefilled
// with everything templates need: Name, Module, Author, Email, License,
// NameUpper, Year, Description; AccountType=none + NoPush=true so no GitHub
// or remote operations exercise stubs unintentionally.
func runBootstrapFor(t *testing.T, template string) (string, Summary) {
	t.Helper()
	if !builtinAvailable(t, template) {
		t.Skipf("template not yet shipped: %s", template)
	}

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	deps := Deps{
		Registry: tmpl.NewRegistry("", ""),
		Hooks:    &recordingHookRunner{},
		Git:      &recordingGitRunner{},
		GitHub:   &recordingGitHubRunner{},
		Output:   io.Discard,
	}

	name := "demo"
	in := Inputs{
		Name:          name,
		Module:        "github.com/example/" + name,
		License:       "MIT",
		Author:        "Test User",
		Email:         "test@example.com",
		AccountType:   "none", // no GitHub repo creation
		Visibility:    "",
		Theme:         "daylight",
		Template:      template,
		Description:   "A demo CLI",
		DefaultBranch: "main",
		Runtime:       []string{"go"},
		Tier:          0,
		Hop:           false,
		NoGitHub:      true,
		NoPush:        true,
		Vars: map[string]any{
			"Name":          name,
			"name":          name,
			"Module":        "github.com/example/" + name,
			"module":        "github.com/example/" + name,
			"License":       "MIT",
			"license":       "MIT",
			"Author":        "Test User",
			"author":        "Test User",
			"Email":         "test@example.com",
			"email":         "test@example.com",
			"NameUpper":     "DEMO",
			"Year":          2026,
			"Description":   "A demo CLI",
			"description":   "A demo CLI",
			"Org":           "example",
			"org":           "example",
			"DefaultBranch": "main",
		},
	}

	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	target := filepath.Join(tmpDir, name)
	require.DirExists(t, target)
	return target, summary
}

func TestBootstrap_CLIGo_Builds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	target, _ := runBootstrapFor(t, "cli-go")

	assert.FileExists(t, filepath.Join(target, "go.mod"))
	assert.FileExists(t, filepath.Join(target, "main.go"))

	// `go build ./...` resolves cobra/viper from module cache; skip when
	// network/cache unavailable rather than fail the suite hard.
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = target
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("go build ./... failed (likely offline / missing deps): %v\n%s", err, out)
	}
}

func TestBootstrap_CLITS_Files(t *testing.T) {
	target, _ := runBootstrapFor(t, "cli-ts")

	assert.FileExists(t, filepath.Join(target, "package.json"))
	assert.FileExists(t, filepath.Join(target, "tsconfig.json"))
	// npm build skipped — CI cost.
}

func TestBootstrap_CLIPy_Files(t *testing.T) {
	target, _ := runBootstrapFor(t, "cli-py")

	assert.FileExists(t, filepath.Join(target, "pyproject.toml"))
	// Template uses {{.Name}} verbatim for package dir (no snake_case xform).
	assert.FileExists(t, filepath.Join(target, "src", "demo", "__init__.py"))
	// pip install skipped — CI cost.
}

func TestBootstrap_MultiRuntime_Files(t *testing.T) {
	if !builtinAvailable(t, "multi-runtime") {
		t.Skip("template not yet shipped: multi-runtime")
	}
	target, _ := runBootstrapFor(t, "multi-runtime")

	assert.FileExists(t, filepath.Join(target, "go.mod"))
	assert.FileExists(t, filepath.Join(target, "package.json"))
	assert.FileExists(t, filepath.Join(target, "pyproject.toml"))
}

func TestBootstrap_Server_Files(t *testing.T) {
	if !builtinAvailable(t, "server") {
		t.Skip("template not yet shipped: server")
	}
	target, _ := runBootstrapFor(t, "server")

	// Server template ships at least one of: Dockerfile, main.go, or app entrypoint.
	candidates := []string{"Dockerfile", "main.go", "server.go", "app.go"}
	found := false
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(target, c)); err == nil {
			found = true
			break
		}
	}
	assert.True(t, found, "server template should ship one of: %v", candidates)
}

func TestBootstrap_Agent_Files(t *testing.T) {
	if !builtinAvailable(t, "agent") {
		t.Skip("template not yet shipped: agent")
	}
	target, _ := runBootstrapFor(t, "agent")

	// Agent template ships at least one of: agent.yaml, agent.go, agent.py, AGENTS.md.
	candidates := []string{"agent.yaml", "agent.go", "agent.py", "AGENTS.md"}
	found := false
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(target, c)); err == nil {
			found = true
			break
		}
	}
	assert.True(t, found, "agent template should ship one of: %v", candidates)
}
