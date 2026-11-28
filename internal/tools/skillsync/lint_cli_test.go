package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThreePartVersions(t *testing.T) {
	for _, c := range []struct {
		line string
		want string
	}{
		{"needs 1.2.3 or later", "1.2.3"},
		{"tag v2.0.10.", "2.0.10"},
		{"kit-1.2.3.tgz", "1.2.3"},
		{"Go 1.27 and Node 22.12+", ""},
		{"host 10.0.0.1 answers", ""},
		{"sha1.2.3 is an identifier", ""},
		{"1.2.3.4", ""},
	} {
		if got := strings.Join(threePartVersions(c.line), ","); got != c.want {
			t.Errorf("%q: got %q, want %q", c.line, got, c.want)
		}
	}
}

func TestMentionsWholePaths(t *testing.T) {
	for _, c := range []struct {
		text, path string
		want       bool
	}{
		{"open references/a.md now", "references/a.md", true},
		{"[x](./references/a.md)", "references/a.md", true},
		{"open references/a.md.", "references/a.md", true},
		{"open references/ab.md", "references/a.md", false},
		{"open xreferences/a.md", "references/a.md", false},
		{"open references/a.mdx", "references/a.md", false},
	} {
		if got := mentions(c.text, c.path); got != c.want {
			t.Errorf("mentions(%q, %q) = %v", c.text, c.path, got)
		}
	}
}

func TestLinkTargetsSkipCode(t *testing.T) {
	got := linkTargets("see [a](x.md), `[b](y.md)` and [c]( <z w.md> ) ![i](img.png \"t\")")
	if strings.Join(got, "|") != "x.md|z w.md|img.png" {
		t.Fatalf("got %q", got)
	}
	if got := linkTargets("[ref]: ./r.md"); len(got) != 1 || got[0] != "./r.md" {
		t.Fatalf("reference definition: %q", got)
	}
}

func TestFenceBlocks(t *testing.T) {
	src := "text\n```sh\na\nb\n```\n~~~\nx\n"
	got := fences([]byte(src))
	if len(got) != 2 || got[0] != (fenceBlock{Open: 2, Lines: 2}) || got[1] != (fenceBlock{Open: 6, Lines: 1}) {
		t.Fatalf("got %+v", got)
	}
}

func lintCLI(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := run(append(append([]string{"lint"}, fixtureArgs(root)...), args...), &o, &e)
	return code, o.String(), e.String()
}

func TestLintCLI(t *testing.T) {
	root := stageLint(t)
	if code, out, errOut := lintCLI(t, root); code != 0 || out != "" || errOut != "" {
		t.Fatalf("clean tree: exit %d, out %q, err %q", code, out, errOut)
	}
	// Findings print as path:line: rule: message, sorted, exit 1.
	edit(t, root, tsDir+"/SKILL.md", "license: MIT\n", "license: MIT\nauthor: x\n")
	edit(t, root, goDir+"/scripts/detect.sh", "#!/bin/sh", "#!/bin/bash")
	code, out, errOut := lintCLI(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], tsDir+"/SKILL.md:6: S1: ") || !strings.HasPrefix(lines[1], goDir+"/scripts/detect.sh:1: S9: ") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(errOut, "2 lint findings") {
		t.Errorf("stderr: %q", errOut)
	}
	// A directory argument limits the run; flags may follow it.
	code, out, _ = lintCLI(t, root, tsDir)
	if code != 1 || strings.Contains(out, "detect.sh") {
		t.Errorf("one dir: exit %d\n%s", code, out)
	}
	code, out, _ = lintCLI(t, root, tsDir, "--standalone")
	if code != 1 || !strings.Contains(out, "S1") {
		t.Errorf("dir then --standalone: exit %d\n%s", code, out)
	}
}

func TestLintCLIUsage(t *testing.T) {
	root := stageLint(t)
	for name, args := range map[string][]string{
		"directory ports.yaml does not name": {"packages/ts"},
		"directory that does not exist":      {"--standalone", "nowhere"},
		"skill directory not written yet":    {"packages/ts/skills/acme-kit-ts/nope"},
	} {
		if code, _, errOut := lintCLI(t, root, args...); code != 2 || errOut == "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, errOut)
		}
	}
	var o, e bytes.Buffer
	if code := run([]string{"sync", "extra"}, &o, &e); code != 2 {
		t.Errorf("sync with a positional: exit %d", code)
	}
}

func TestLintInconsistentInputsExitOne(t *testing.T) {
	root := stageLint(t)
	edit(t, root, "ports.yaml", "no_skill: [tasks]", "no_skill: []")
	if code, _, errOut := lintCLI(t, root); code != 1 || !strings.Contains(errOut, "tasks") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

// With no skill present the lint passes: nothing to check is not a failure.
func TestLintNoSkillsPresent(t *testing.T) {
	root := stage(t, "none")
	if code, out, errOut := lintCLI(t, root); code != 0 || out != "" {
		t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
	}
}

// The lint runs every rule against this repository's own tree. While no
// skill exists that is vacuous for S1 to S9, and it still proves that the
// repository carries no marketplace file and that ports.yaml, the publish
// matrix and the parity page agree.
func TestLintRealTree(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not inside the repository")
	}
	found, err := Lint(DefaultOptions(root), LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("%s", f)
	}
	// S5 maps reference slugs to parity sections by title; the page must
	// still have every one.
	g, err := loadInputs(DefaultOptions(root), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range areaTitles {
		ok := false
		for _, s := range g.par.Sections {
			ok = ok || s.Title == a.Title
		}
		if !ok {
			t.Errorf("the parity page has no section %q (slug %s)", a.Title, a.Slug)
		}
	}
	if len(areaTitles) != len(g.par.Sections) {
		t.Errorf("the parity page has %d sections, S5 knows %d", len(g.par.Sections), len(areaTitles))
	}
}
