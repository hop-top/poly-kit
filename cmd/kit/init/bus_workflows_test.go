// Integration tests for --with-bus-workflows wiring (T-0776).
//
// Drives runBootstrap and runAugment directly with WithBusWorkflows set
// and asserts the four .github/workflows/kit-bus-*.yml files (or their
// dry-run plan entries) land where the spec says they should.
package kitinit

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/cmd/kit/init/buswf"
	tmpl "hop.top/kit/internal/template"
)

// busInputs builds an Inputs struct that bootstraps a minimal Go
// project under cli-go and opts into bus workflows.
func busInputs(name string, withBus bool) Inputs {
	return Inputs{
		Name:             name,
		Module:           "github.com/example/" + name,
		License:          "MIT",
		Author:           "Test User",
		Email:            "test@example.com",
		AccountType:      "none",
		Theme:            "daylight",
		Template:         "cli-go",
		Description:      "demo",
		DefaultBranch:    "main",
		Runtime:          []string{"go"},
		Tier:             0,
		Hop:              false,
		NoGitHub:         true,
		NoPush:           true,
		WithBusWorkflows: withBus,
		Vars: map[string]any{
			"Name":          name,
			"name":          name,
			"Module":        "github.com/example/" + name,
			"module":        "github.com/example/" + name,
			"License":       "MIT",
			"Author":        "Test User",
			"Email":         "test@example.com",
			"NameUpper":     "DEMO",
			"Year":          2026,
			"Description":   "demo",
			"Org":           "example",
			"DefaultBranch": "main",
		},
	}
}

// TestBootstrap_WithBusWorkflows_WritesFiles bootstraps cli-go with
// the bus flag and asserts all four .github/workflows/kit-bus-*.yml
// files exist + the manifest tracks them.
func TestBootstrap_WithBusWorkflows_WritesFiles(t *testing.T) {
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
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
	in := busInputs("demo", true)
	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	target := filepath.Join(tmpDir, "demo")
	for _, f := range buswf.Files() {
		assert.FileExists(t, filepath.Join(target, ".github", "workflows", f.Name))
	}

	// Manifest exists with four entries.
	mfPath := filepath.Join(target, ".kit", "generated.json")
	require.FileExists(t, mfPath)
	m, err := buswf.ReadManifest(target)
	require.NoError(t, err)
	assert.Len(t, m.Files, 4, "manifest should track all four kit-bus workflows")

	// Summary surfaces the plan entries.
	assert.Len(t, summary.BusWorkflows, 4)
	for _, e := range summary.BusWorkflows {
		assert.Equal(t, buswf.ActionWrite, e.Action)
		assert.Equal(t, buswf.ReasonNew, e.Reason)
	}
}

// TestBootstrap_WithoutBusWorkflows_DoesNotWriteFiles verifies the
// default-off behavior: kit init without --with-bus-workflows does
// not scaffold any kit-bus workflow.
func TestBootstrap_WithoutBusWorkflows_DoesNotWriteFiles(t *testing.T) {
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
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
	in := busInputs("demo", false)
	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	target := filepath.Join(tmpDir, "demo")
	for _, f := range buswf.Files() {
		_, statErr := os.Stat(filepath.Join(target, ".github", "workflows", f.Name))
		assert.True(t, os.IsNotExist(statErr),
			"%s should NOT exist when --with-bus-workflows is off", f.Name)
	}
	assert.Empty(t, summary.BusWorkflows,
		"summary.BusWorkflows should be empty when flag is off")
}

// TestBootstrap_WithBusWorkflows_DryRun: dry-run reports the plan but
// touches no files.
func TestBootstrap_WithBusWorkflows_DryRun(t *testing.T) {
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
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
	in := busInputs("demo", true)
	in.DryRun = true
	summary, err := runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	// No bus workflow files on disk.
	target := filepath.Join(tmpDir, "demo")
	for _, f := range buswf.Files() {
		_, statErr := os.Stat(filepath.Join(target, ".github", "workflows", f.Name))
		assert.True(t, os.IsNotExist(statErr), "%s leaked through dry-run", f.Name)
	}
	// But the plan reflects all four would be written.
	assert.Len(t, summary.BusWorkflows, 4)
}
