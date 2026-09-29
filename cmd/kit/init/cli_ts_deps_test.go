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
	"gopkg.in/yaml.v3"

	tmpl "hop.top/kit/internal/template"
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

// TestBootstrap_CLITS_CommanderAndNodeFloor pins the commander major the
// generated project builds on, and the Node floor that major needs.
// The app hands its commander Command to @hop-top/kit (e.g.
// registerOutputFlags), so both sides share commander 15; commander 15
// is ESM-only and declares engines.node >=22.12, the floor that also
// lets the CJS kit SDK require() it. The scaffold's .npmrc sets
// engine-strict, so the project states the same floor.
func TestBootstrap_CLITS_CommanderAndNodeFloor(t *testing.T) {
	_, pkg, _ := renderCLITSPackageJSON(t)

	assert.Regexp(t, `^\^15\.`, pkg.Dependencies["commander"],
		"generated project depends on commander 15, the major @hop-top/kit builds on")
	assert.Equal(t, ">=22.12", pkg.Engines["node"],
		"generated project declares the Node floor commander 15 requires")
}

// cliTSAllowedBuilds are the dependencies whose install scripts a
// generated cli-ts project runs: esbuild (vitest's bundler) fetches its
// platform binary, better-sqlite3 (@hop-top/kit) its native addon.
// pnpm 11 refuses any other build script (strictDepBuilds), so a new
// native dependency fails the install until it is reviewed here.
var cliTSAllowedBuilds = map[string]bool{
	"better-sqlite3": true,
	"esbuild":        true,
}

// TestBootstrap_CLITS_AllowsItsDependencyBuilds pins the pnpm 11 build
// allowlist a generated project ships. Without it the first `pnpm
// install` exits ERR_PNPM_IGNORED_BUILDS. pnpm 11 reads allowBuilds
// from pnpm-workspace.yaml only; package.json and .npmrc are ignored.
func TestBootstrap_CLITS_AllowsItsDependencyBuilds(t *testing.T) {
	target, _ := runBootstrapFor(t, "cli-ts")

	raw, err := os.ReadFile(filepath.Join(target, "pnpm-workspace.yaml"))
	require.NoError(t, err, "generated project ships pnpm-workspace.yaml")
	var ws struct {
		AllowBuilds map[string]bool `yaml:"allowBuilds"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &ws), "pnpm-workspace.yaml is valid YAML:\n%s", raw)
	assert.Equal(t, cliTSAllowedBuilds, ws.AllowBuilds,
		"allowBuilds allows exactly the dependency builds the project needs")
}

// TestCLITSTiers_PNPMWorkspaceShipsWithPackageJSON keeps the build
// allowlist on every tier that writes package.json: at a tier with the
// manifest but without the allowlist, `pnpm install` fails again.
func TestCLITSTiers_PNPMWorkspaceShipsWithPackageJSON(t *testing.T) {
	root := kitRepoRoot(t)
	for _, dir := range []string{"templates/cli-ts", "internal/template/builtins/cli-ts"} {
		tiers, err := tmpl.LoadTiers(os.DirFS(filepath.Join(root, dir)))
		require.NoError(t, err)
		require.NotEmpty(t, tiers["package.json"], "%s/tiers.yaml maps package.json", dir)
		assert.Equal(t, tiers["package.json"], tiers["pnpm-workspace.yaml"],
			"%s/tiers.yaml ships pnpm-workspace.yaml on package.json's tiers", dir)
	}
}
