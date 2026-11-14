package file

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetPreservesModeAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	p := filepath.Join(dir, "k")
	if err := os.WriteFile(p, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), "k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("stray files: %v", ents)
	}
}

func TestWriteAtomicFailureKeepsOldContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "k")
	if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	// Directory not writable: temp creation fails, target intact.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	if err := New(dir, nil).Set(context.Background(), "k", []byte("new")); err == nil {
		t.Fatal("want error")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "old" {
		t.Fatalf("content = %q, want old", b)
	}
}

func TestWriteAtomicRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	// Target is a non-empty directory: rename of a file onto it fails.
	p := filepath.Join(dir, "k")
	if err := os.MkdirAll(filepath.Join(p, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(p, []byte("new")); err == nil {
		t.Fatal("want error")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "k" {
		t.Fatalf("temp not cleaned: %v", ents)
	}
}

func TestSetThroughSymlinkKeepsLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	real := filepath.Join(t.TempDir(), "real")
	if err := os.WriteFile(real, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "k")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := New(dir, nil).Set(context.Background(), "k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	li, err := os.Lstat(link)
	if err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", li, err)
	}
	b, _ := os.ReadFile(real)
	if string(b) != "new" {
		t.Fatalf("target = %q, want new", b)
	}
	ti, _ := os.Stat(real)
	if ti.Mode().Perm() != 0640 {
		t.Fatalf("target mode = %v, want 0640", ti.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(real))
	if len(ents) != 1 {
		t.Fatalf("stray files next to target: %v", ents)
	}
}

func TestWriteAtomicSyncsParentDir(t *testing.T) {
	var got []string
	old := syncDir
	syncDir = func(d string) error { got = append(got, d); return nil }
	defer func() { syncDir = old }()
	dir := t.TempDir()
	if err := writeAtomic(filepath.Join(dir, "k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != dir {
		t.Fatalf("syncDir calls = %v, want [%s]", got, dir)
	}
}

func TestSyncDirReal(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
