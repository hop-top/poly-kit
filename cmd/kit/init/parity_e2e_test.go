package kitinit_test

// parity_e2e_test.go — verifies that the Go template engine and the
// standalone bash init.sh produce byte-identical output trees from the
// same kit-template.yaml + input variables.
//
// The fixture is self-contained (LICENSE-MIT + LICENSE-Apache-2.0
// embedded directly so the Go engine can satisfy the license rule
// without shared/ composition). Token-substitution and tier filtering
// are out of scope — this test exercises only the render_rules
// post-render pipeline.
//
// Skipped under -short and when bash/yq aren't on PATH.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmpl "hop.top/kit/internal/template"
)

const parityManifest = `name: parity-fixture
description: "synthetic fixture for go/bash parity test"
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
  remove_after_render:
    - "kit-template.yaml"
    - "tiers.yaml"
  license:
    var: License
    target: LICENSE
    sources:
      MIT: LICENSE-MIT
      Apache-2.0: LICENSE-Apache-2.0
hooks: {}
`

// parityFiles is the source tree shared by both render paths. Keys are
// project-relative source paths; values are file contents. The bash and
// Go pipelines must produce identical output trees from this input.
var parityFiles = map[string]string{
	"kit-template.yaml":  parityManifest,
	"tiers.yaml":         "files:\n  \"main.go\": [0, 1, 2, 3, 4]\n",
	"main.go.tmpl":       "package main // hello\n",
	"README.md.tmpl":     "# README\n",
	"LICENSE-MIT":        "MIT body\n",
	"LICENSE-Apache-2.0": "Apache body\n",
}

func TestParity_GoEngineVsBashInitSh(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to bash; skip under -short")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("yq"); err != nil {
		t.Skip("yq not on PATH (required by init.sh)")
	}

	// 1. Render via Go engine into goTarget.
	goTarget := t.TempDir()
	src := fstest.MapFS{}
	for rel, body := range parityFiles {
		src[rel] = &fstest.MapFile{Data: []byte(body)}
	}
	manifestPath := filepath.Join(t.TempDir(), "kit-template.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(parityManifest), 0o644))
	manifest, err := tmpl.Parse(manifestPath)
	require.NoError(t, err)
	require.NoError(t, manifest.Validate())

	tiers, err := tmpl.LoadTiers(src)
	require.NoError(t, err)

	eng := tmpl.NewEngineWithRules(
		src, goTarget,
		map[string]any{"License": "MIT"},
		manifest.Files,
		manifest.RenderRules,
		tiers,
		0, false,
	)
	_, err = eng.Render(context.Background())
	require.NoError(t, err)

	// 2. Render via bash init.sh into bashTarget.
	bashParent := t.TempDir()
	bashTarget := filepath.Join(bashParent, "demo")
	require.NoError(t, os.MkdirAll(bashTarget, 0o755))
	for rel, body := range parityFiles {
		full := filepath.Join(bashTarget, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	// init.sh + lib.sh from shared/
	repoRoot := findRepoRoot(t)
	initSrc := filepath.Join(repoRoot, "internal", "template", "builtins", "shared", "init.sh")
	libSrc := filepath.Join(repoRoot, "templates", "lib.sh")
	require.NoError(t, copyFileTo(initSrc, filepath.Join(bashTarget, "init.sh")))
	require.NoError(t, copyFileTo(libSrc, filepath.Join(bashParent, "lib.sh")))
	require.NoError(t, os.Chmod(filepath.Join(bashTarget, "init.sh"), 0o755))

	home := t.TempDir()
	cmd := exec.Command("bash", "init.sh")
	cmd.Dir = bashTarget
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=x",
		"GIT_AUTHOR_EMAIL=x@x.com",
		"GIT_COMMITTER_NAME=x",
		"GIT_COMMITTER_EMAIL=x@x.com",
		"INIT_APP_NAME=hello",
		"INIT_DESCRIPTION=demo",
		"INIT_AUTHOR_NAME=x",
		"INIT_AUTHOR_EMAIL=x@x.com",
		"INIT_LICENSE=MIT",
		"INIT_MODULE_PREFIX=github.com/x",
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("bash init.sh failed: %v\n%s", runErr, out)
	}

	// 3. init.sh additionally creates .git/, runs initial commit, and
	// removes itself. Strip those before comparing — they're outside
	// the render_rules pipeline and the Go engine never produces them.
	require.NoError(t, os.RemoveAll(filepath.Join(bashTarget, ".git")))
	_ = os.Remove(filepath.Join(bashTarget, "init.sh"))
	_ = os.Remove(filepath.Join(bashParent, "lib.sh"))

	// 4. Compare the two trees byte-for-byte.
	goTree := snapshotTree(t, goTarget)
	bashTree := snapshotTree(t, bashTarget)

	assert.Equal(t, goTree, bashTree,
		"go engine and bash init.sh must produce byte-identical output")
}

// snapshotTree walks dir and returns map[relpath]sha256 for every file.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		out[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	}))
	// Stable string for assert.Equal diff messages (map ordering otherwise random).
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	stable := map[string]string{}
	for _, k := range keys {
		stable[k] = out[k]
	}
	return stable
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
