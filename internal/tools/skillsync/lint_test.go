package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// skillDirs are the fixture skill directories, by skill name.
var skillDirs = map[string]string{
	"acme-kit":     "skills/acme-kit",
	"acme-kit-go":  "skills/acme-kit-go",
	"acme-kit-ts":  "packages/ts/skills/acme-kit-ts",
	"acme-kit-py":  "packages/py/skills/acme-kit-py",
	"acme-kit-rs":  "packages/rs/skills/acme-kit-rs",
	"acme-kit-php": "packages/php/skills/acme-kit-php",
}

// fixtureOptions are the generator options of a staged fixture root.
func fixtureOptions(root string) Options {
	return Options{Root: root, Parity: "parity.md", Publish: "publish.yml", Tiers: "tiers.md", Manifest: "manifest.json", Ports: "ports.yaml"}
}

// stageLint builds the clean lint fixture: the hand-written skills and SDK
// trees under testdata/lint/in, completed by the generator itself so the
// generated files are real output, never hand-made copies.
func stageLint(t *testing.T) string {
	t.Helper()
	root := stage(t, "lint")
	o := fixtureOptions(root)
	pl, err := BuildPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Write(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func lintAll(t *testing.T, root string, lo LintOptions) []string {
	t.Helper()
	found, err := Lint(fixtureOptions(root), lo)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.String()
	}
	return out
}

func TestCleanFixturesHaveNoFindings(t *testing.T) {
	root := stageLint(t)
	if got := lintAll(t, root, LintOptions{}); len(got) > 0 {
		t.Fatalf("clean fixtures yield findings:\n%s", strings.Join(got, "\n"))
	}
	for name, dir := range skillDirs {
		if got := lintAll(t, root, LintOptions{Standalone: true, Dirs: []string{dir}}); len(got) > 0 {
			t.Errorf("%s: standalone findings on a clean skill:\n%s", name, strings.Join(got, "\n"))
		}
	}
	if code, _, errOut := runCLI(t, root, "sync", "--check"); code != 0 {
		t.Errorf("the clean fixtures are not what sync produces (exit %d): %s", code, errOut)
	}
}

// want is one expected finding. File is relative to the skill directory (""
// is the directory itself), or to the root when Abs. The line is Line, else
// the first line of the file containing Anchor, else 1.
type want struct {
	File   string
	Anchor string
	Line   int
	Rule   string
	Abs    bool
}

type mutation struct {
	Copy   [][2]string // directory copies, root-relative from to
	Write  map[string]string
	Edits  [][3]string // root-relative path, old, new
	Remove []string
	Chmod  map[string]fs.FileMode
}

type lintCase struct {
	Name       string
	Skill      string // skill name; default acme-kit-ts
	Dir        string // directory to lint when it is not the skill's own
	Standalone bool
	Mut        mutation
	Want       []want
}

func (m mutation) apply(t *testing.T, root string) {
	t.Helper()
	for _, c := range m.Copy {
		copyTree(t, filepath.Join(root, c[0]), filepath.Join(root, c[1]))
	}
	for p, content := range m.Write {
		dst := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range m.Edits {
		edit(t, root, e[0], e[1], e[2])
	}
	for _, p := range m.Remove {
		if err := os.RemoveAll(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	for p, mode := range m.Chmod {
		if err := os.Chmod(filepath.Join(root, p), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func (c lintCase) skillDir() string {
	if c.Dir != "" {
		return c.Dir
	}
	n := c.Skill
	if n == "" {
		n = "acme-kit-ts"
	}
	return skillDirs[n]
}

// expected renders the wanted findings as path:line: rule, sorted.
func (c lintCase) expected(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, w := range c.Want {
		rel := filepath.ToSlash(filepath.Join(c.skillDir(), w.File))
		if w.Abs {
			rel = w.File
		}
		line := w.Line
		if line == 0 {
			line = 1
			if w.Anchor != "" {
				line = lineOf(t, root, rel, w.Anchor)
			}
		}
		out = append(out, fmt.Sprintf("%s:%d: %s", rel, line, w.Rule))
	}
	sort.Strings(out)
	return out
}

// where is a finding line without its message: path:line: rule.
func where(finding string) string {
	parts := strings.SplitN(finding, ": ", 3)
	return parts[0] + ": " + parts[1]
}

// check lints the mutated tree and requires exactly the wanted findings,
// compared by path, line and rule.
func (c lintCase) check(t *testing.T) {
	t.Helper()
	root := stageLint(t)
	c.Mut.apply(t, root)
	lo := LintOptions{Standalone: c.Standalone}
	if c.Standalone {
		lo.Dirs = []string{c.skillDir()}
	}
	got := lintAll(t, root, lo)
	have := make([]string, len(got))
	for i, g := range got {
		have[i] = where(g)
	}
	sort.Strings(have)
	want := c.expected(t, root)
	if strings.Join(have, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings differ\n got:\n%s\nwant:\n%s\nfull output:\n%s", strings.Join(have, "\n"), strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func runCases(t *testing.T, cases []lintCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.Name, c.check)
	}
}
