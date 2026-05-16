package kitinit_test

// initsh_e2e_test.go — exercises the standalone init.sh that ships in
// the rendered scaffold. Verifies post-run that:
//   1. No `.tmpl`-suffixed files remain (T-1051).
//   2. kit-template.yaml + tiers.yaml are removed (T-1052).
//   3. Core targets (main.go, go.mod, Makefile) survive token substitution.
//
// Layout mirrors templates/scaffold.sh: the rendered project dir lives
// under a tmp parent, and lib.sh is placed at that parent so init.sh's
// `source "$SCRIPT_DIR/../lib.sh"` resolves.
//
// Skipped under `go test -short` since it shells out and requires bash + git.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitSh_StripsTmplAndManifests(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to bash + git; skip under -short")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// Locate repo root by climbing from this test file to the dir
	// holding go.mod (kit module root).
	repoRoot := findRepoRoot(t)
	cliGoSrc := filepath.Join(repoRoot, "internal", "template", "builtins", "cli-go")
	sharedSrc := filepath.Join(repoRoot, "internal", "template", "builtins", "shared")
	libSrc := filepath.Join(repoRoot, "templates", "lib.sh")

	for _, p := range []string{cliGoSrc, sharedSrc, libSrc} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("required input missing: %s: %v", p, err)
		}
	}

	parent := t.TempDir()
	project := filepath.Join(parent, "demo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	// Copy cli-go template tree into the project dir.
	if err := copyTree(cliGoSrc, project); err != nil {
		t.Fatalf("copy cli-go: %v", err)
	}

	// Copy init.sh + LICENSE-* from shared/ — init.sh strips .tmpl
	// from the LICENSE files, then copies one over LICENSE.
	for _, name := range []string{"init.sh", "LICENSE-MIT.tmpl", "LICENSE-Apache-2.0.tmpl"} {
		if err := copyFile(filepath.Join(sharedSrc, name), filepath.Join(project, name)); err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}
	if err := os.Chmod(filepath.Join(project, "init.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Place lib.sh at the parent so `source "$SCRIPT_DIR/../lib.sh"` resolves.
	if err := copyFile(libSrc, filepath.Join(parent, "lib.sh")); err != nil {
		t.Fatalf("copy lib.sh: %v", err)
	}

	// Isolate HOME so init.sh's git config + commit can't touch the host.
	home := t.TempDir()

	cmd := exec.Command("bash", "init.sh")
	cmd.Dir = project
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=x",
		"GIT_AUTHOR_EMAIL=x@x.com",
		"GIT_COMMITTER_NAME=x",
		"GIT_COMMITTER_EMAIL=x@x.com",
		"INIT_APP_NAME=demo",
		"INIT_DESCRIPTION=demo project",
		"INIT_AUTHOR_NAME=x",
		"INIT_AUTHOR_EMAIL=x@x.com",
		"INIT_LICENSE=MIT",
		"INIT_MODULE_PREFIX=github.com/x",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init.sh failed: %v\n%s", err, out)
	}

	// Walk the project dir; collect any .tmpl leftovers.
	var leftovers []string
	if walkErr := filepath.WalkDir(project, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip the .git tree init.sh creates — it has its own packed
		// content that may legitimately contain "tmpl" substrings,
		// though never as a literal suffix.
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".tmpl") {
			rel, _ := filepath.Rel(project, path)
			leftovers = append(leftovers, rel)
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if len(leftovers) > 0 {
		t.Errorf("expected zero .tmpl files after init.sh, found:\n  %s",
			strings.Join(leftovers, "\n  "))
	}

	// Manifests must be removed.
	for _, rel := range []string{"kit-template.yaml", "tiers.yaml"} {
		if _, err := os.Stat(filepath.Join(project, rel)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed by init.sh, stat err: %v", rel, err)
		}
	}

	// Core rendered files must exist.
	for _, rel := range []string{"main.go", "go.mod", "Makefile"} {
		if _, err := os.Stat(filepath.Join(project, rel)); err != nil {
			t.Errorf("expected %s after init.sh: %v", rel, err)
		}
	}
}

// findRepoRoot walks upward from this test file's location until it
// finds a go.mod, returning the directory that contains it.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found above test file)")
		}
		dir = parent
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
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
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
