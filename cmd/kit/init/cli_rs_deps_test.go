// The cli-rs template depends on the Rust kit SDK (hop-top-kit), and a
// generated project is only as current as that requirement. It tracks
// the sdk/experimental/rs release the template ships with: the kit-rs
// release PR bumps it, the same way the kit-ts release PR bumps the
// cli-ts range.
package kitinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kitRSCrate is the crates.io name of the Rust kit SDK.
const kitRSCrate = "hop-top-kit"

// cliRSPinFiles hold the cli-rs template's kit requirement, in the
// source tree and in the embedded mirror, relative to the repo root.
var cliRSPinFiles = []string{
	"templates/cli-rs/Cargo.toml.tmpl",
	"internal/template/builtins/cli-rs/Cargo.toml.tmpl",
}

// rsDocPinFiles are adopter docs whose install snippets require the
// Rust kit SDK; the kit-rs release PR bumps them too.
var rsDocPinFiles = []string{
	"docs/adopters/reference/rs-sdk.md",
	"docs/adopters/guides/serve-mcp-from-any-sdk.md",
}

// releasedKitRSVersion reads sdk/experimental/rs's version from the
// release-please manifest: the kit-rs release this checkout is, or the
// one its release PR is cutting.
func releasedKitRSVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kitRepoRoot(t), ".github", ".release-please-manifest.json"))
	require.NoError(t, err)
	var manifest map[string]string
	require.NoError(t, json.Unmarshal(raw, &manifest))
	v := manifest["sdk/experimental/rs"]
	require.NotEmpty(t, v, "the manifest carries sdk/experimental/rs's version")
	return v
}

func TestBootstrap_CLIRS_DependsOnTheKitReleaseItShipsWith(t *testing.T) {
	want := releasedKitRSVersion(t)
	// CrateName defaults to Name; kit init resolves it before rendering.
	target, _ := runBootstrapFor(t, "cli-rs", map[string]any{"CrateName": "demo"})

	raw, err := os.ReadFile(filepath.Join(target, "Cargo.toml"))
	require.NoError(t, err)
	var manifest struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	_, err = toml.Decode(string(raw), &manifest)
	require.NoError(t, err, "generated Cargo.toml is valid TOML:\n%s", raw)
	kit, ok := manifest.Dependencies[kitRSCrate].(map[string]any)
	require.True(t, ok, "generated Cargo.toml declares %s as a table:\n%s", kitRSCrate, raw)

	// A bare prerelease requirement is a caret requirement Cargo
	// resolves to that prerelease or a later one on the same line.
	assert.Equal(t, want, kit["version"],
		"a generated project depends on the kit-rs release that generated it")
	assert.NotContains(t, string(raw), releaseMarker,
		"the release annotation is a template comment and must not reach the project")
}

// TestCLIRSKitPin_BumpedByTheReleasePR pins the release wiring: every
// file holding the requirement is a root-relative generic extra-file of
// the sdk/experimental/rs package and carries the marker on the
// requirement's own line. Drop either and a kit-rs release leaves the
// requirement behind.
func TestCLIRSKitPin_BumpedByTheReleasePR(t *testing.T) {
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
	for _, entry := range cfg.Packages["sdk/experimental/rs"].ExtraFiles {
		var f struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if json.Unmarshal(entry, &f) == nil && f.Type == "generic" {
			generic[f.Path] = true
		}
	}

	for _, path := range cliRSPinFiles {
		// Leading slash: release-please resolves the path from the
		// repo root instead of from sdk/experimental/rs.
		assert.True(t, generic["/"+path],
			"/%s must be a generic extra-file of the sdk/experimental/rs package", path)

		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		var marked []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, releaseMarker) {
				marked = append(marked, line)
			}
		}
		if assert.Len(t, marked, 1, "%s marks exactly one line", path) {
			assert.Contains(t, marked[0], kitRSCrate+` = { version = "`,
				"%s marks the kit requirement's own line", path)
		}
	}
}

// TestRSDocPins_BumpedByTheReleasePR pins the docs' release wiring:
// each doc is a root-relative generic extra-file of the
// sdk/experimental/rs package, every hop-top-kit requirement in it
// carries the marker, and each cites the current kit-rs release.
func TestRSDocPins_BumpedByTheReleasePR(t *testing.T) {
	root := kitRepoRoot(t)
	want := releasedKitRSVersion(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "release-please-config.json"))
	require.NoError(t, err)
	var cfg struct {
		Packages map[string]struct {
			ExtraFiles []json.RawMessage `json:"extra-files"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	generic := map[string]bool{}
	for _, entry := range cfg.Packages["sdk/experimental/rs"].ExtraFiles {
		var f struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if json.Unmarshal(entry, &f) == nil && f.Type == "generic" {
			generic[f.Path] = true
		}
	}

	for _, path := range rsDocPinFiles {
		assert.True(t, generic["/"+path],
			"/%s must be a generic extra-file of the sdk/experimental/rs package", path)

		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		pins := 0
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, kitRSCrate+` = { version = "`) {
				continue
			}
			pins++
			assert.Contains(t, line, "# "+releaseMarker, "%s: pin carries the marker: %s", path, line)
			assert.Contains(t, line, `version = "`+want+`"`, "%s: pin cites kit-rs %s: %s", path, want, line)
		}
		assert.NotZero(t, pins, "%s holds no hop-top-kit pin; drop it from rsDocPinFiles", path)
	}
}
