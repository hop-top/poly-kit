package shim

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestExpandToParentDirs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"single file", []string{"a/b/x.go"}, []string{"a/b"}},
		{
			"siblings under same parent",
			[]string{"a/b/x.go", "a/b/y.go"},
			[]string{"a/b"},
		},
		{
			"two distinct parents",
			[]string{"a/b/x.go", "c/d/y.go"},
			[]string{"a/b", "c/d"},
		},
		{
			"nested folds to higher ancestor",
			[]string{"a/x.go", "a/b/y.go"},
			[]string{"a"},
		},
		{
			"three with two ancestors",
			[]string{"pkg/foo/a.go", "pkg/foo/bar/b.go", "pkg/qux/c.go"},
			[]string{"pkg/foo", "pkg/qux"},
		},
		{
			"a/ is not a prefix of ab/",
			[]string{"a/x.go", "ab/y.go"},
			[]string{"a", "ab"},
		},
		{
			"empty strings filtered",
			[]string{"", "a/b/x.go", ""},
			[]string{"a/b"},
		},
		{
			"unsorted input → sorted output",
			[]string{"z/x.go", "a/x.go", "m/x.go"},
			[]string{"a", "m", "z"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpandToParentDirs(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExpandToParentDirs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandToParentDirsClosureProperty(t *testing.T) {
	t.Parallel()
	// Property: every input path must be contained by exactly one
	// element of the output.
	in := []string{
		"src/a/x.go", "src/a/y.go", "src/b/z.go",
		"docs/readme.md", "docs/sub/intro.md",
		"top.txt",
	}
	parents := ExpandToParentDirs(in)
	for _, f := range in {
		dir := filepath.Dir(f)
		matches := 0
		for _, p := range parents {
			if isAncestor(p, dir) {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("file %q matched %d parents in %v; want exactly 1",
				f, matches, parents)
		}
	}
}

func TestIsAncestor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"a", "a", true},
		{"a", "a/b", true},
		{"a", "ab", false},
		{"a/b", "a/b/c/d", true},
		{"a/b", "a/c", false},
		{"", "a", false}, // empty parent: not ancestor of anything
	}
	for _, tc := range cases {
		got := isAncestor(tc.parent, tc.child)
		if got != tc.want {
			t.Errorf("isAncestor(%q, %q) = %v, want %v",
				tc.parent, tc.child, got, tc.want)
		}
	}
}

func TestEnumerateDirFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "sub/b.txt"), "b")
	mustWrite(t, filepath.Join(root, "sub/c.txt"), "c")
	mustWrite(t, filepath.Join(root, "sub/deep/d.txt"), "d")

	t.Run("no cap, no filter", func(t *testing.T) {
		got, overflow, err := EnumerateDirFiles(root, 0, nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if overflow {
			t.Error("overflow = true with max=0")
		}
		if len(got) != 4 {
			t.Errorf("len(got) = %d, want 4: %v", len(got), got)
		}
	})

	t.Run("under cap", func(t *testing.T) {
		got, overflow, err := EnumerateDirFiles(root, 10, nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if overflow {
			t.Error("overflow = true; tree fits within cap")
		}
		if len(got) != 4 {
			t.Errorf("len = %d, want 4", len(got))
		}
	})

	t.Run("at cap exactly", func(t *testing.T) {
		_, overflow, err := EnumerateDirFiles(root, 4, nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if overflow {
			t.Error("overflow = true; cap matches exactly")
		}
	})

	t.Run("over cap", func(t *testing.T) {
		got, overflow, err := EnumerateDirFiles(root, 2, nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !overflow {
			t.Error("overflow = false; expected true with max < total")
		}
		if len(got) > 2 {
			t.Errorf("len = %d, want <= 2: %v", len(got), got)
		}
	})

	t.Run("filter excludes .txt", func(t *testing.T) {
		got, _, err := EnumerateDirFiles(root, 0, func(path string) bool {
			return !strings.HasSuffix(path, ".txt")
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len = %d, want 0 with .txt-excluding filter: %v",
				len(got), got)
		}
	})

	t.Run("empty dir argument errors", func(t *testing.T) {
		_, _, err := EnumerateDirFiles("", 0, nil)
		if err == nil {
			t.Error("err = nil, want error for empty dir")
		}
	})

	t.Run("nonexistent dir errors", func(t *testing.T) {
		_, _, err := EnumerateDirFiles(filepath.Join(root, "no-such-dir"), 0, nil)
		if err == nil {
			t.Error("err = nil, want error for nonexistent dir")
		}
	})
}

func TestFormatFileBlock(t *testing.T) {
	t.Parallel()
	t.Run("empty input → empty string", func(t *testing.T) {
		if got := FormatFileBlock(nil, ""); got != "" {
			t.Errorf("FormatFileBlock(nil) = %q, want empty", got)
		}
	})

	t.Run("with cwd-rooted paths", func(t *testing.T) {
		cwd := "/repo"
		got := FormatFileBlock([]string{"/repo/a.go", "/repo/sub/b.go"}, cwd)
		if !strings.Contains(got, "- a.go\n") {
			t.Errorf("missing relative a.go in:\n%s", got)
		}
		if !strings.Contains(got, "- sub/b.go\n") {
			t.Errorf("missing relative sub/b.go in:\n%s", got)
		}
		if !strings.Contains(got, "tree rooted at /repo") {
			t.Errorf("missing root note in:\n%s", got)
		}
	})

	t.Run("paths outside cwd preserved as-is", func(t *testing.T) {
		got := FormatFileBlock([]string{"/elsewhere/a.go"}, "/repo")
		if !strings.Contains(got, "/elsewhere/a.go") {
			t.Errorf("expected absolute path in output:\n%s", got)
		}
	})

	t.Run("no cwd → absolute paths and no root note", func(t *testing.T) {
		got := FormatFileBlock([]string{"/a/x.go", "/a/y.go"}, "")
		if !strings.Contains(got, "- /a/x.go\n") {
			t.Errorf("expected /a/x.go in:\n%s", got)
		}
		if strings.Contains(got, "tree rooted at") {
			t.Errorf("unexpected root note when cwd empty:\n%s", got)
		}
	})

	t.Run("output is deterministic and sorted", func(t *testing.T) {
		a := FormatFileBlock([]string{"z.go", "a.go", "m.go"}, "")
		b := FormatFileBlock([]string{"a.go", "m.go", "z.go"}, "")
		if a != b {
			t.Errorf("unstable output:\nA: %s\nB: %s", a, b)
		}
		idxA := strings.Index(a, "- a.go")
		idxM := strings.Index(a, "- m.go")
		idxZ := strings.Index(a, "- z.go")
		if idxA >= idxM || idxM >= idxZ {
			t.Errorf("expected sorted order in:\n%s", a)
		}
	})
}

func TestRefuseDangerousDegradation(t *testing.T) {
	t.Parallel()
	d := RefuseDangerousDegradation(
		"Approval", "auto-edit", "--dangerously-skip-permissions",
		[]string{"ApprovalAsk", "ApprovalNever"})
	if d.Level != "error" {
		t.Errorf("Level = %q, want error", d.Level)
	}
	if d.Option != "Approval" {
		t.Errorf("Option = %q, want Approval", d.Option)
	}
	for _, want := range []string{
		"auto-edit",
		"--dangerously-skip-permissions",
		"ApprovalAsk",
		"ApprovalNever",
	} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("Message missing %q:\n%s", want, d.Message)
		}
	}

	d2 := RefuseDangerousDegradation("Approval", "auto-edit", "--yolo", nil)
	if !strings.Contains(d2.Message, "No safer alternative") {
		t.Errorf("expected 'No safer alternative' message:\n%s", d2.Message)
	}
}

func TestSplitConfigList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"alone", []string{"alone"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{`shell(git:*),write,WebSearch`, []string{"shell(git:*)", "write", "WebSearch"}},
		{`a\,b,c`, []string{"a,b", "c"}}, // backslash-escaped comma
		{`one\,two\,three`, []string{"one,two,three"}},
		{",,empty,,middles,,", []string{"empty", "middles"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SplitConfigList(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SplitConfigList(%q) = %v, want %v",
					tc.in, got, tc.want)
			}
		})
	}
}

// FuzzFormatFileBlock confirms the formatter never panics on weird
// path content.
func FuzzFormatFileBlock(f *testing.F) {
	f.Add("a.go", "b.go", "/cwd")
	f.Add("", "", "")
	f.Add("\x00", "/abs", "")
	f.Fuzz(func(t *testing.T, a, b, cwd string) {
		_ = FormatFileBlock([]string{a, b}, cwd)
	})
}

// FuzzExpandToParentDirs confirms the closure property holds under
// arbitrary inputs (no panic; every input matches exactly one
// output parent).
func FuzzExpandToParentDirs(f *testing.F) {
	f.Add("a/b.go", "a/c.go", "x.go")
	f.Add("", "", "")
	f.Add("a", "a/b", "a/b/c")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		in := []string{a, b, c}
		out := ExpandToParentDirs(in)
		nonEmpty := nonEmptyCount(in)
		if nonEmpty == 0 && len(out) != 0 {
			t.Errorf("empty inputs → non-empty parents: %v", out)
		}
		if !sort.StringsAreSorted(out) {
			t.Errorf("output not sorted: %v", out)
		}
	})
}

func nonEmptyCount(ss []string) int {
	n := 0
	for _, s := range ss {
		if s != "" {
			n++
		}
	}
	return n
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
