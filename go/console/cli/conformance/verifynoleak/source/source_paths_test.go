package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
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

	got, err := Paths(dir, []string{"stories"}, scanner.SupportedPath)
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
	got, err := Paths(dir, []string{"notes.md"}, scanner.SupportedPath)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(got) != 1 || got[0] != md {
		t.Fatalf("got %v, want [%s]", got, md)
	}
}

// A directory that resolves but holds no scannable file is not an
// error — an all-Go tree is legitimately clean. The vacuous scan is
// surfaced by the command layer's "0 files scanned" stderr warning
// instead, which keeps exit 0 (see TestVerifyNoLeak_PathsZeroScannedWarns).
func TestPathsEmptyDirectoryIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Paths(dir, []string{"empty"}, scanner.SupportedPath)
	if err != nil {
		t.Fatalf("resolvable empty directory must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no files, got %v", got)
	}
}

// A symlink-to-directory root must expand the same as a real
// directory. Paths classifies the root with os.Stat (which follows
// symlinks) before handing it to expandDir; expandDir must follow
// suit for the root instead of silently yielding zero files.
func TestPathsExpandsSymlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "a.yaml")
	if err := os.WriteFile(target, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	got, err := Paths(dir, []string{"link"}, scanner.SupportedPath)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expanded %d file(s) through symlinked root, want 1: %v", len(got), got)
	}
}

// A missing path stays an error: explicit means intentional.
func TestPathsMissingErrors(t *testing.T) {
	if _, err := Paths(t.TempDir(), []string{"nope"}, scanner.SupportedPath); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}

// An unresolvable path must carry ErrBadPaths so the command layer
// classifies it as a config error. io_error is excluded from the
// conformance action's fail-on set, so misrouting this would let a
// typo'd --paths pass CI silently.
func TestPathsUnusableWrapErrBadPaths(t *testing.T) {
	dir := t.TempDir()
	for name, arg := range map[string]string{
		"missing":                     "nope",
		"missing under existing root": filepath.Join("sub", "nope.yaml"),
	} {
		_, err := Paths(dir, []string{arg}, scanner.SupportedPath)
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if !errors.Is(err, ErrBadPaths) {
			t.Errorf("%s: error %v does not wrap ErrBadPaths", name, err)
		}
	}
}
