package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A directory passed to --paths used to be returned verbatim, so the
// scanner skipped it as an unsupported extension and reported zero
// scanned files while still exiting clean — a pass that measured
// nothing. Directories must expand to the scannable files beneath.
func TestPathsExpandsDirectory(t *testing.T) {
	dir := t.TempDir()
	stories := filepath.Join(dir, "stories")
	if err := os.MkdirAll(filepath.Join(stories, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string) string {
		full := filepath.Join(stories, rel)
		if err := os.WriteFile(full, []byte("id: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	a := write("a.yaml")
	b := write("b.yml")
	nested := write(filepath.Join("nested", "c.yaml"))
	write("notes.txt")

	got, err := Paths(dir, []string{"stories"})
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	want := map[string]bool{a: true, b: true, nested: true}
	if len(got) != len(want) {
		t.Fatalf("expanded %d file(s), want %d: %v", len(got), len(want), got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

// Explicit file arguments must keep passing through untouched,
// including files the directory walk would filter out by extension.
func TestPathsKeepsExplicitFiles(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Paths(dir, []string{"notes.md"})
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(got) != 1 || got[0] != md {
		t.Fatalf("got %v, want [%s]", got, md)
	}
}

// An empty directory cannot substantiate a clean scan; surfacing it
// as an error beats reporting zero scanned files and exiting clean.
func TestPathsEmptyDirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Paths(dir, []string{"empty"}); err == nil {
		t.Fatal("expected an error for a directory with no scannable files")
	}
}

// A missing path stays an error: explicit means intentional.
func TestPathsMissingErrors(t *testing.T) {
	if _, err := Paths(t.TempDir(), []string{"nope"}); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}

// Both unusable-path failures must carry ErrBadPaths so the command
// layer can classify them as config errors. io_error is excluded
// from the conformance action's fail-on set, so misrouting these
// would let an unscannable --paths pass CI silently.
func TestPathsUnusableWrapErrBadPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, arg := range map[string]string{
		"missing":         "nope",
		"empty directory": "empty",
	} {
		_, err := Paths(dir, []string{arg})
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if !errors.Is(err, ErrBadPaths) {
			t.Errorf("%s: error %v does not wrap ErrBadPaths", name, err)
		}
	}
}
