package file_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"hop.top/kit/go/storage/secret/file"
)

func TestRegressionDeepNesting(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	keys := []string{
		"a/b/c/d",
		"a/b/c/e",
		"a/b/f",
		"a/g",
		"a/b/c/d2/e/f",
	}
	for _, k := range keys {
		if err := s.Set(ctx, k, []byte("v")); err != nil {
			t.Fatalf("Set(%q): %v", k, err)
		}
	}

	tests := []struct {
		prefix string
		want   []string
	}{
		{"", keys},
		{"a/", keys},
		{"a/b/", []string{"a/b/c/d", "a/b/c/d2/e/f", "a/b/c/e", "a/b/f"}},
		{"a/b/c/", []string{"a/b/c/d", "a/b/c/d2/e/f", "a/b/c/e"}},
		{"a/b/c/d2", []string{"a/b/c/d2/e/f"}},
		{"a/g", []string{"a/g"}},
		{"a/b/c/d2/e/f", []string{"a/b/c/d2/e/f"}},
		{"z/", nil},
	}

	for _, tt := range tests {
		got, err := s.List(ctx, tt.prefix)
		if err != nil {
			t.Fatalf("List(%q): %v", tt.prefix, err)
		}
		sort.Strings(got)
		want := sorted(tt.want)
		if !slicesEqual(got, want) {
			t.Errorf("List(%q) = %v, want %v", tt.prefix, got, want)
		}
	}
}

func TestRegressionSymlinkFileEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require privileges on Windows")
	}

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret")
	if err := os.WriteFile(outsideFile, []byte("leaked"), 0600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "legit", []byte("ok"))

	if err := os.Symlink(outsideFile, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}

	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "escape" {
			t.Fatal("List included symlinked file pointing outside root")
		}
	}
	if len(keys) != 1 || keys[0] != "legit" {
		t.Fatalf("got %v, want [legit]", keys)
	}
}

func TestRegressionSymlinkDirEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require privileges on Windows")
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "leaked"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "safe", []byte("ok"))

	if err := os.Symlink(outside, filepath.Join(dir, "escape_dir")); err != nil {
		t.Fatal(err)
	}

	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "escape_dir/leaked" || k == "escape_dir" {
			t.Fatalf("List surfaced key %q from symlinked directory", k)
		}
	}
	if len(keys) != 1 || keys[0] != "safe" {
		t.Fatalf("got %v, want [safe]", keys)
	}
}

func TestRegressionPrefixBoundary(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "config/db", []byte("v1"))
	_ = s.Set(ctx, "config/dbx", []byte("v2"))
	_ = s.Set(ctx, "config/dba/host", []byte("v3"))

	tests := []struct {
		prefix string
		want   []string
	}{
		// "config/db" must NOT match "config/dbx" (prefix boundary)
		{"config/db", []string{"config/db"}},
		{"config/dbx", []string{"config/dbx"}},
		{"config/dba", []string{"config/dba/host"}},
		{"config/dba/", []string{"config/dba/host"}},
		{"config/", []string{"config/db", "config/dba/host", "config/dbx"}},
	}

	for _, tt := range tests {
		got, err := s.List(ctx, tt.prefix)
		if err != nil {
			t.Fatalf("List(%q): %v", tt.prefix, err)
		}
		sort.Strings(got)
		want := sorted(tt.want)
		if !slicesEqual(got, want) {
			t.Errorf("List(%q) = %v, want %v", tt.prefix, got, want)
		}
	}
}

func TestRegressionHiddenFileSkip(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "visible", []byte("ok"))

	// hidden file at root
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	// hidden dir with file
	hiddenDir := filepath.Join(dir, ".secretdir")
	if err := os.MkdirAll(hiddenDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, "key"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	// hidden file inside visible dir
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".dotfile"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == ".hidden" || k == ".secretdir/key" || k == "sub/.dotfile" {
			t.Fatalf("List surfaced hidden key %q", k)
		}
	}
	if len(keys) != 1 || keys[0] != "visible" {
		t.Fatalf("got %v, want [visible]", keys)
	}
}

func TestRegressionTrailingSlashStorePath(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir+"/", nil)
	ctx := context.Background()

	_ = s.Set(ctx, "a/b", []byte("v1"))
	_ = s.Set(ctx, "c", []byte("v2"))

	// List works
	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	want := []string{"a/b", "c"}
	if !slicesEqual(keys, want) {
		t.Fatalf("List(\"\") = %v, want %v", keys, want)
	}

	// Get works
	got, err := s.Get(ctx, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "v1" {
		t.Fatalf("Get(a/b) = %q, want %q", got.Value, "v1")
	}

	// List with prefix works
	keys, err = s.List(ctx, "a/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "a/b" {
		t.Fatalf("List(a/) = %v, want [a/b]", keys)
	}
}

// helpers

func sorted(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
