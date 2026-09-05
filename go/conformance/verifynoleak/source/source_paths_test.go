package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/conformance/verifynoleak/scanner"
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

// A regular file removed between os.ReadDir listing a directory and
// the walk classifying that entry must not abort the whole --paths
// scan the way the earlier e.Info()-per-entry implementation did:
// e.Info() issued a fresh Lstat per regular-file candidate, so a file
// removed in that window returned ENOENT and the walk hard-failed
// even though nothing about the caller's request was actually wrong.
// Non-symlink entries are now classified via e.Type(), which ReadDir
// already buffered — no syscall, so there's no window left to race at
// all for this case. Driven with a background goroutine racing the
// directory listing against the removal so this pins "the race window
// is gone", not just "a file absent before the walk started is
// skipped" (which the pre-fix code also handled fine).
func TestPathsToleratesRegularFileRemovedDuringWalk(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.yaml")
	if err := os.WriteFile(keep, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.yaml")
	if err := os.WriteFile(gone, []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			// Recreate + remove in a tight loop so at least one
			// iteration lands inside the walk's ReadDir-to-classify
			// window, regardless of scheduling.
			_ = os.WriteFile(gone, []byte("id: y\n"), 0o644)
			_ = os.Remove(gone)
		}
	}()
	defer func() {
		<-done
		_ = os.WriteFile(gone, []byte("id: y\n"), 0o644) // leave deterministic state
	}()

	for i := range 200 {
		got, err := Paths(dir, []string{"."}, scanner.SupportedPath)
		if err != nil {
			t.Fatalf("Paths: %v (iteration %d)", err, i)
		}
		found := false
		for _, p := range got {
			if p == keep {
				found = true
			}
		}
		if !found {
			t.Fatalf("iteration %d: got %v, missing %s", i, got, keep)
		}
	}
}

// A symlink entry removed entirely between os.ReadDir listing it and
// e.Info() resolving its target is the one case that can still race
// in walkDirEntries — e.Info() Lstats the symlink itself, which is a
// real syscall unlike the buffered e.Type() path regular files take.
// Under PathsAllowingMissing that race must drop only the raced
// symlink, not abort the walk or lose the sibling file already found;
// under strict Paths it must still abort with ErrBadPaths (never
// silently tolerated without --allow-missing-paths).
func TestPathsAllowingMissingToleratesSymlinkRemovedDuringWalk(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.yaml")
	if err := os.WriteFile(keep, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "link-target.yaml")
	if err := os.WriteFile(target, []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "gone-link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_ = os.Symlink(target, link)
			_ = os.Remove(link)
		}
	}()
	defer func() {
		<-done
		_ = os.Symlink(target, link) // leave deterministic state
	}()

	for i := range 200 {
		got, err := PathsAllowingMissing(dir, []string{"."}, scanner.SupportedPath)
		if err != nil {
			t.Fatalf("PathsAllowingMissing: %v (iteration %d)", err, i)
		}
		found := false
		for _, p := range got {
			if p == keep {
				found = true
			}
		}
		if !found {
			t.Fatalf("iteration %d: got %v, missing %s", i, got, keep)
		}
	}
}

// A broken symlink inside a --paths directory is silently excluded
// rather than erroring: DirEntry.Info() Lstats the symlink itself
// (mode ModeSymlink), so it never follows through to the missing
// target and never fails — the same "skip, don't fail" outcome
// filepath.WalkDir gave pre-fix. Pinned so a future change that makes
// the symlink branch follow the link (e.g. os.Stat instead of
// e.Info()) doesn't silently start hard-failing scans over a broken
// link nobody asked --paths to resolve.
func TestPathsBrokenSymlinkIsSilentlyExcluded(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.yaml")
	if err := os.WriteFile(keep, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), broken); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	got, err := Paths(dir, []string{"."}, scanner.SupportedPath)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(got) != 1 || got[0] != keep {
		t.Fatalf("got %v, want [%s] (broken symlink must be excluded, not erroring)", got, keep)
	}
}

// A symlinked directory nested below a --paths root is intentionally
// not followed, unlike the root itself — matching verify-stories.
// Regression coverage for that documented asymmetry: a future change
// to nested-entry handling should have to touch this test to change
// the behavior either way.
func TestPathsDoesNotExpandNestedSymlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "hidden.yaml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(root, "visible.yaml")
	if err := os.WriteFile(visible, []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "nested-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	got, err := Paths(dir, []string{"root"}, scanner.SupportedPath)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(got) != 1 || got[0] != visible {
		t.Fatalf("got %v, want [%s] (nested symlinked dir must not be followed)", got, visible)
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

// walkPaths (the --audit no-git fallback) must follow a symlinked
// root the same way expandDir does for --paths: filepath.WalkDir
// Lstats its root argument, so a symlinked cwd used to report as
// neither a directory nor a regular file and yield zero files while
// still exiting clean. Called directly rather than through Audit, so
// this doesn't depend on Audit's git-repo detection actually missing
// a .git ancestor in the test environment.
func TestWalkPathsFollowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "a.yaml")
	if err := os.WriteFile(target, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	got, err := walkPaths(link)
	if err != nil {
		t.Fatalf("walkPaths: %v", err)
	}
	found := false
	for _, p := range got {
		if filepath.Base(p) == "a.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("walked %v through symlinked root, want to find a.yaml", got)
	}
}

// A subdirectory removed between its parent's os.ReadDir listing it
// and the recursive walkDirEntries call reaching it must drop only
// that subtree under PathsAllowingMissing, not the files already
// found in sibling subdirectories walked before it — losing an
// already-collected sibling would be a much larger silent under-scan
// than the single-entry tolerance --allow-missing-paths documents.
func TestPathsAllowingMissingToleratesSubdirRemovedDuringWalk(t *testing.T) {
	dir := t.TempDir()
	keepDir := filepath.Join(dir, "aaa-keep")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(keepDir, "keep.yaml")
	if err := os.WriteFile(keep, []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goneDir := filepath.Join(dir, "zzz-gone")
	if err := os.MkdirAll(goneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goneDir, "y.yaml"), []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_ = os.MkdirAll(goneDir, 0o755)
			_ = os.RemoveAll(goneDir)
		}
	}()
	defer func() {
		<-done
		_ = os.MkdirAll(goneDir, 0o755) // leave deterministic state
	}()

	for i := range 200 {
		got, err := PathsAllowingMissing(dir, []string{"."}, scanner.SupportedPath)
		if err != nil {
			t.Fatalf("PathsAllowingMissing: %v (iteration %d)", err, i)
		}
		found := false
		for _, p := range got {
			if p == keep {
				found = true
			}
		}
		if !found {
			t.Fatalf("iteration %d: got %v, missing sibling %s", i, got, keep)
		}
	}
}
