// The cli-php template requires the PHP kit SDK (hop-top/kit), and a
// generated project is only as current as that constraint. It tracks
// the sdk/experimental/php release the template ships with: the kit-php
// release PR bumps it, the same way the kit-ts release PR bumps the
// cli-ts range.
package kitinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kitPHPPackage is the Composer name of the PHP kit SDK.
const kitPHPPackage = "hop-top/kit"

// cliPHPPinFiles hold the cli-php template's kit constraint, in the
// source tree and in the embedded mirror, relative to the repo root.
var cliPHPPinFiles = []string{
	"templates/cli-php/composer.json.tmpl",
	"internal/template/builtins/cli-php/composer.json.tmpl",
}

type composerJSON struct {
	Require map[string]string `json:"require"`
}

// releasedKitPHPVersion reads sdk/experimental/php's version from the
// release-please manifest: the kit-php release this checkout is, or the
// one its release PR is cutting.
func releasedKitPHPVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kitRepoRoot(t), ".github", ".release-please-manifest.json"))
	require.NoError(t, err)
	var manifest map[string]string
	require.NoError(t, json.Unmarshal(raw, &manifest))
	v := manifest["sdk/experimental/php"]
	require.NotEmpty(t, v, "the manifest carries sdk/experimental/php's version")
	return v
}

// renderCLIPHPComposerJSON bootstraps cli-php and parses the generated
// composer.json; a parse failure means the template renders invalid JSON.
func renderCLIPHPComposerJSON(t *testing.T) (composerJSON, string) {
	t.Helper()
	// kit init resolves these manifest defaults before rendering.
	target, _ := runBootstrapFor(t, "cli-php", map[string]any{
		"Vendor":          "example",
		"VendorNamespace": "Example",
		"NameNamespace":   "Demo",
	})
	raw, err := os.ReadFile(filepath.Join(target, "composer.json"))
	require.NoError(t, err)
	var pkg composerJSON
	require.NoError(t, json.Unmarshal(raw, &pkg), "generated composer.json is valid JSON:\n%s", raw)
	return pkg, string(raw)
}

func TestBootstrap_CLIPHP_RequiresTheKitReleaseItShipsWith(t *testing.T) {
	want := releasedKitPHPVersion(t)
	pkg, raw := renderCLIPHPComposerJSON(t)

	// A caret constraint on a prerelease lets Composer take that
	// prerelease or a later one on the same line; it infers the
	// stability from the constraint, so no @alpha flag is needed.
	assert.Equal(t, "^"+want, pkg.Require[kitPHPPackage],
		"a generated project requires the kit-php release that generated it")
	assert.NotContains(t, raw, releaseMarker,
		"the release annotation is a template comment and must not reach the project")
}

// TestBootstrap_CLIPHP_PHPFloorMatchesTheSDK holds the generated
// project's PHP floor to the one hop-top/kit itself requires: a lower
// floor promises a PHP the SDK refuses to install on.
func TestBootstrap_CLIPHP_PHPFloorMatchesTheSDK(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(kitRepoRoot(t), "sdk", "experimental", "php", "composer.json"))
	require.NoError(t, err)
	var sdk composerJSON
	require.NoError(t, json.Unmarshal(raw, &sdk))
	require.NotEmpty(t, sdk.Require["php"], "the SDK declares a PHP floor")

	pkg, _ := renderCLIPHPComposerJSON(t)
	assert.Equal(t, sdk.Require["php"], pkg.Require["php"],
		"a generated project requires the PHP the kit SDK requires")
}

// TestCLIPHPKitPin_BumpedByTheReleasePR pins the release wiring: every
// file holding the constraint is a root-relative generic extra-file of
// the sdk/experimental/php package and carries the marker on the
// constraint's own line. Drop either and a kit-php release leaves the
// constraint behind.
func TestCLIPHPKitPin_BumpedByTheReleasePR(t *testing.T) {
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
	for _, entry := range cfg.Packages["sdk/experimental/php"].ExtraFiles {
		var f struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if json.Unmarshal(entry, &f) == nil && f.Type == "generic" {
			generic[f.Path] = true
		}
	}

	for _, path := range cliPHPPinFiles {
		// Leading slash: release-please resolves the path from the
		// repo root instead of from sdk/experimental/php.
		assert.True(t, generic["/"+path],
			"/%s must be a generic extra-file of the sdk/experimental/php package", path)

		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		var marked []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, releaseMarker) {
				marked = append(marked, line)
			}
		}
		if assert.Len(t, marked, 1, "%s marks exactly one line", path) {
			assert.Contains(t, marked[0], `"`+kitPHPPackage+`": "^`,
				"%s marks the kit constraint's own line", path)
		}
	}
}
