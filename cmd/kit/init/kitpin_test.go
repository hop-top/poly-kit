// The cli-go template pins hop.top/kit in its go.mod, and a project
// kit init generates is only as current as that pin. These tests hold
// the pin to the release the template ships with: equal to the root
// package's version in the release-please manifest, which the release
// PR bumps together with the pin, so the two can never drift apart
// through a release.
package kitinit

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tmpl "hop.top/kit/internal/template"
)

// releaseMarker is release-please's generic-updater annotation: the
// release PR rewrites the version on every line that carries it.
const releaseMarker = "x-release-please-version"

// cliGoPinFiles are the files holding the cli-go template's kit pin,
// in the source tree and in the embedded mirror, relative to the repo
// root.
var cliGoPinFiles = []string{
	"templates/cli-go/go.mod.tmpl",
	"templates/cli-go/kit-template.yaml",
	"internal/template/builtins/cli-go/go.mod.tmpl",
	"internal/template/builtins/cli-go/kit-template.yaml",
}

// releasedKitVersion reads the root package's version from the
// release-please manifest: the kit release this checkout is, or the
// one its release PR is cutting.
func releasedKitVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kitRepoRoot(t), ".github", ".release-please-manifest.json"))
	require.NoError(t, err)
	var manifest map[string]string
	require.NoError(t, json.Unmarshal(raw, &manifest))
	v := manifest["."]
	require.NotEmpty(t, v, "the manifest carries the root package's version")
	return v
}

func TestBootstrap_CLIGo_PinsTheKitReleaseItShipsWith(t *testing.T) {
	want := releasedKitVersion(t)
	target, _ := runBootstrapFor(t, "cli-go")

	gomod, err := os.ReadFile(filepath.Join(target, "go.mod"))
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^\thop\.top/kit v`+regexp.QuoteMeta(want)+`$`, string(gomod),
		"a generated project requires exactly the kit release that generated it")
	assert.NotContains(t, string(gomod), releaseMarker,
		"the release annotation is a template comment and must not reach the project")

	// kit_version is dropped at render; read it off the embedded
	// manifest, which is what `kit template list` shows.
	builtins, err := tmpl.BuiltIn()
	require.NoError(t, err)
	manifest, err := fs.ReadFile(builtins, "cli-go/kit-template.yaml")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^kit_version: ">=`+regexp.QuoteMeta(want)+`"`, string(manifest),
		"the template needs the kit release it ships with")
}

// TestCLIGoKitPin_BumpedByTheReleasePR pins the release wiring: every
// file holding the pin is an extra-file of the root package with the
// generic updater, and carries the marker on the pin's own line. Drop
// either and a release leaves the pin behind again.
func TestCLIGoKitPin_BumpedByTheReleasePR(t *testing.T) {
	root := kitRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "release-please-config.json"))
	require.NoError(t, err)
	var cfg struct {
		Packages map[string]struct {
			ExtraFiles []json.RawMessage `json:"extra-files"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	generic := map[string]bool{}
	for _, entry := range cfg.Packages["."].ExtraFiles {
		var f struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if json.Unmarshal(entry, &f) == nil && f.Type == "generic" {
			generic[f.Path] = true
		}
	}

	for _, path := range cliGoPinFiles {
		assert.True(t, generic[path],
			"%s must be a generic extra-file of the root package", path)

		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		var marked []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, releaseMarker) {
				marked = append(marked, line)
			}
		}
		if assert.Len(t, marked, 1, "%s marks exactly one line", path) {
			assert.Regexp(t, `hop\.top/kit v|^kit_version: `, marked[0],
				"%s marks the pin's own line", path)
		}
	}
}
