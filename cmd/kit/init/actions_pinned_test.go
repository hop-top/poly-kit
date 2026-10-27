// Scaffold-level guard: every workflow kit init writes into an adopter
// repo pins its `uses:` refs to commit SHAs, checked by the same
// scripts/lint-actions-pinned.sh that gates this repo's own workflows.
package kitinit

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/cmd/kit/init/buswf"
	tmpl "hop.top/kit/internal/template"
)

// TestBootstrap_GeneratedWorkflowsArePinned bootstraps a scaffold with
// every workflow generator on (all runtimes' CI, test callers, publish,
// release-please, bus) and runs the pin lint over the result.
func TestBootstrap_GeneratedWorkflowsArePinned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lint-actions-pinned.sh needs bash + awk")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if !builtinAvailable(t, "cli-go") {
		t.Skip("cli-go template not available")
	}
	lint, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "lint-actions-pinned.sh"))
	require.NoError(t, err)

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
	in.Runtime = []string{"go", "ts", "py", "rs", "php"}
	in.Vars["Runtime"] = in.Runtime
	in.WithGitHubWorkflows = true
	in.WithReleasePlease = true
	_, err = runBootstrap(context.Background(), deps, in)
	require.NoError(t, err)

	target := filepath.Join(tmpDir, "demo")
	wfs, err := filepath.Glob(filepath.Join(target, ".github", "workflows", "*.yml"))
	require.NoError(t, err)

	// Each generator must have contributed, or the lint proves nothing.
	names := map[string]bool{}
	for _, wf := range wfs {
		names[filepath.Base(wf)] = true
	}
	want := []string{"release-please.yml", "test-go-caller.yml"}
	for _, rt := range in.Runtime {
		want = append(want, "ci-"+rt+".yml")
	}
	for _, f := range buswf.Files() {
		want = append(want, f.Name)
	}
	for _, w := range want {
		assert.True(t, names[w], "scaffold missing .github/workflows/%s (have %v)", w, names)
	}

	out, err := exec.Command("bash", append([]string{lint}, wfs...)...).CombinedOutput()
	assert.NoError(t, err, "generated workflows carry unpinned uses: refs:\n%s", out)

	// Every pinned ref names its version for Dependabot and readers.
	for _, wf := range wfs {
		b, rerr := os.ReadFile(wf)
		require.NoError(t, rerr)
		for i, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			if !strings.HasPrefix(l, "uses:") || strings.Contains(l, "uses: ./") {
				continue
			}
			assert.Contains(t, l, " # ", "%s:%d: pinned ref without a version comment: %s",
				filepath.Base(wf), i+1, l)
		}
	}

	dbot, err := os.ReadFile(filepath.Join(target, ".github", "dependabot.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(dbot), "package-ecosystem: github-actions",
		"scaffold dependabot.yml must keep the action pins current")
}
