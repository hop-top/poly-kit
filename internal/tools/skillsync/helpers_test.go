package main

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata/<case>/want")

// stage copies testdata/common and testdata/<name>/in into a temp root.
func stage(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "common"), root)
	copyTree(t, filepath.Join("testdata", name, "in"), root)
	return root
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(filepath.Join(dst, rel), b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fixtureArgs points the tool at the flat fixture inputs.
func fixtureArgs(root string, extra ...string) []string {
	return append([]string{
		"--root", root,
		"--parity", "parity.md",
		"--publish", "publish.yml",
		"--tiers", "tiers.md",
		"--manifest", "manifest.json",
		"--ports", "ports.yaml",
	}, extra...)
}

func runCLI(t *testing.T, root string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	rest := append(fixtureArgs(root), args[1:]...)
	code = run(append([]string{args[0]}, rest...), &o, &e)
	return code, o.String(), e.String()
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		m[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// changed returns the files new or different in after relative to before.
func changed(before, after map[string]string) map[string]string {
	out := map[string]string{}
	for p, c := range after {
		if old, ok := before[p]; !ok || old != c {
			out[p] = c
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func compareGolden(t *testing.T, name string, got map[string]string) {
	t.Helper()
	want := filepath.Join("testdata", name, "want")
	if *update {
		if err := os.RemoveAll(want); err != nil {
			t.Fatal(err)
		}
		for p, c := range got {
			dst := filepath.Join(want, p)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	wantFiles := map[string]string{}
	if _, err := os.Stat(want); err == nil {
		wantFiles = snapshot(t, want)
	}
	if len(got) == 0 {
		t.Fatalf("%s: nothing was written; a golden over no output proves nothing", name)
	}
	if a, b := strings.Join(sortedKeys(got), "\n"), strings.Join(sortedKeys(wantFiles), "\n"); a != b {
		t.Fatalf("%s: written files differ\n got:\n%s\nwant:\n%s", name, a, b)
	}
	for _, p := range sortedKeys(got) {
		if got[p] != wantFiles[p] {
			t.Errorf("%s: %s differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, p, got[p], wantFiles[p])
		}
	}
}

// edit replaces old with new in root/rel and fails when old is absent, so a
// mutation can never silently do nothing.
func edit(t *testing.T, root, rel, old, repl string) {
	t.Helper()
	p := filepath.Join(root, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lineOf(t *testing.T, root, rel, needle string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	t.Fatalf("%s: no line contains %q", rel, needle)
	return 0
}
