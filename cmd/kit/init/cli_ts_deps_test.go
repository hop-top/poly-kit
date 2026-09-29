// The cli-ts template's source imports packages its package.json has
// to declare, or a generated project fails to resolve them on the
// first build. The kit SDK is one of them, and its range tracks the
// sdk/ts release the template ships with: the kit-ts release PR bumps
// it, the same way the root release PR bumps the cli-go pin.
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
)

// kitTSPackage is the npm name of the TypeScript kit SDK (sdk/ts).
const kitTSPackage = "@hop-top/kit"

// cliTSPinFiles hold the cli-ts template's kit range, in the source
// tree and in the embedded mirror, relative to the repo root.
var cliTSPinFiles = []string{
	"templates/cli-ts/package.json.tmpl",
	"internal/template/builtins/cli-ts/package.json.tmpl",
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
}

// renderCLITSPackageJSON bootstraps cli-ts and parses the generated
// package.json; a parse failure means the template renders invalid JSON.
func renderCLITSPackageJSON(t *testing.T) (string, packageJSON, string) {
	t.Helper()
	target, _ := runBootstrapFor(t, "cli-ts")
	raw, err := os.ReadFile(filepath.Join(target, "package.json"))
	require.NoError(t, err)
	var pkg packageJSON
	require.NoError(t, json.Unmarshal(raw, &pkg), "generated package.json is valid JSON:\n%s", raw)
	return target, pkg, string(raw)
}

// releasedKitTSVersion reads sdk/ts's version from the release-please
// manifest: the kit-ts release this checkout is, or the one its
// release PR is cutting.
func releasedKitTSVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kitRepoRoot(t), ".github", ".release-please-manifest.json"))
	require.NoError(t, err)
	var manifest map[string]string
	require.NoError(t, json.Unmarshal(raw, &manifest))
	v := manifest["sdk/ts"]
	require.NotEmpty(t, v, "the manifest carries sdk/ts's version")
	return v
}

// importSpecifier matches the module specifier of static `import ...
// from "x"`, side-effect `import "x"` and `export ... from "x"` forms.
var importSpecifier = regexp.MustCompile(`(?m)^\s*(?:import|export)\s(?:[^'"]*?\sfrom\s)?["']([^"']+)["']`)

// packageName maps a bare module specifier to the npm package that
// provides it: "@scope/pkg/sub" -> "@scope/pkg", "pkg/sub" -> "pkg".
func packageName(spec string) string {
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func TestBootstrap_CLITS_DeclaresEveryPackageItImports(t *testing.T) {
	target, pkg, _ := renderCLITSPackageJSON(t)

	checked := 0
	err := filepath.WalkDir(filepath.Join(target, "src"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return walkErr
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range importSpecifier.FindAllStringSubmatch(string(body), -1) {
			spec := m[1]
			if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "node:") {
				continue
			}
			checked++
			name := packageName(spec)
			_, dep := pkg.Dependencies[name]
			_, dev := pkg.DevDependencies[name]
			assert.True(t, dep || dev,
				"%s imports %q; package.json must declare %q", filepath.Base(path), spec, name)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, checked, "no bare imports found under src/ — walk is miswired")
}

func TestBootstrap_CLITS_DependsOnTheKitReleaseItShipsWith(t *testing.T) {
	want := releasedKitTSVersion(t)
	_, pkg, raw := renderCLITSPackageJSON(t)

	assert.Equal(t, "^"+want, pkg.Dependencies[kitTSPackage],
		"a generated project depends on the kit-ts release that generated it")
	assert.NotContains(t, raw, releaseMarker,
		"the release annotation is a template comment and must not reach the project")
}

// TestCLITSKitPin_BumpedByTheReleasePR pins the release wiring: every
// file holding the range is a root-relative generic extra-file of the
// sdk/ts package and carries the marker on the range's own line. Drop
// either and a kit-ts release leaves the range behind.
func TestCLITSKitPin_BumpedByTheReleasePR(t *testing.T) {
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
	for _, entry := range cfg.Packages["sdk/ts"].ExtraFiles {
		var f struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if json.Unmarshal(entry, &f) == nil && f.Type == "generic" {
			generic[f.Path] = true
		}
	}

	for _, path := range cliTSPinFiles {
		// Leading slash: release-please resolves the path from the
		// repo root instead of from sdk/ts.
		assert.True(t, generic["/"+path],
			"/%s must be a generic extra-file of the sdk/ts package", path)

		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		var marked []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, releaseMarker) {
				marked = append(marked, line)
			}
		}
		if assert.Len(t, marked, 1, "%s marks exactly one line", path) {
			assert.Contains(t, marked[0], `"`+kitTSPackage+`": "^`,
				"%s marks the kit range's own line", path)
		}
	}
}
