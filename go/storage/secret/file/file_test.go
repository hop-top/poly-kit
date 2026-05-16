package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/file"
)

// mockKeeper XORs with a fixed byte for symmetric encrypt/decrypt.
type mockKeeper struct{ key byte }

func (m *mockKeeper) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	out := make([]byte, len(plaintext))
	for i, b := range plaintext {
		out[i] = b ^ m.key
	}
	return out, nil
}

func (m *mockKeeper) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	return m.Encrypt(context.TODO(), ciphertext) // XOR is symmetric
}

func TestPlaintextRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	if err := s.Set(ctx, "db_pass", []byte("s3cr3t")); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "db_pass")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "s3cr3t" {
		t.Fatalf("got %q, want %q", got.Value, "s3cr3t")
	}
}

func TestEncryptedRoundtrip(t *testing.T) {
	dir := t.TempDir()
	k := &mockKeeper{key: 0xAB}
	s := file.New(dir, k)
	ctx := context.Background()

	if err := s.Set(ctx, "api_key", []byte("token123")); err != nil {
		t.Fatal(err)
	}

	// raw file should NOT be plaintext
	raw, _ := os.ReadFile(filepath.Join(dir, "api_key"))
	if string(raw) == "token123" {
		t.Fatal("file stored as plaintext despite keeper")
	}

	got, err := s.Get(ctx, "api_key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "token123" {
		t.Fatalf("got %q, want %q", got.Value, "token123")
	}
}

func TestGet_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)

	_, err := s.Get(context.Background(), "nope")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestNestedKey(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	if err := s.Set(ctx, "nested/key", []byte("deep")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "nested/key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "deep" {
		t.Fatalf("got %q, want %q", got.Value, "deep")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "db_host", []byte("localhost"))
	_ = s.Set(ctx, "db_port", []byte("5432"))
	_ = s.Set(ctx, "cache_ttl", []byte("60"))

	keys, err := s.List(ctx, "db_")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2: %v", len(keys), keys)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "exists", []byte("yes"))

	ok, err := s.Exists(ctx, "exists")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}

	ok, err = s.Exists(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "todel", []byte("x"))
	if err := s.Delete(ctx, "todel"); err != nil {
		t.Fatal(err)
	}
	ok, _ := s.Exists(ctx, "todel")
	if ok {
		t.Fatal("expected deleted")
	}
}

func TestDelete_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)

	err := s.Delete(context.Background(), "ghost")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestPathTraversal(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	for _, key := range []string{"../etc/passwd", "../../etc/shadow", "../outside"} {
		if err := s.Set(ctx, key, []byte("pwned")); err == nil {
			t.Fatalf("Set(%q) should fail with traversal error", key)
		}
		if _, err := s.Get(ctx, key); err == nil {
			t.Fatalf("Get(%q) should fail with traversal error", key)
		}
		if _, err := s.Exists(ctx, key); err == nil {
			t.Fatalf("Exists(%q) should fail with traversal error", key)
		}
		if err := s.Delete(ctx, key); err == nil || errors.Is(err, secret.ErrNotFound) {
			t.Fatalf("Delete(%q) should fail with traversal error", key)
		}
	}
}

func TestListNested_regressions(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "top", []byte("v0"))
	_ = s.Set(ctx, "a/b", []byte("v1"))
	_ = s.Set(ctx, "a/d/c", []byte("v2"))
	_ = s.Set(ctx, "a/deep", []byte("v5"))
	_ = s.Set(ctx, "a/other", []byte("v3"))
	_ = s.Set(ctx, "x/y", []byte("v4"))

	tests := []struct {
		prefix string
		want   []string
	}{
		{"", []string{"a/b", "a/d/c", "a/deep", "a/other", "top", "x/y"}},
		{"a/", []string{"a/b", "a/d/c", "a/deep", "a/other"}},
		{"a/d", []string{"a/d/c"}},
		{"a/d/", []string{"a/d/c"}},
		{"x/", []string{"x/y"}},
		{"top", []string{"top"}},
		{"nope", nil},
	}

	for _, tt := range tests {
		keys, err := s.List(ctx, tt.prefix)
		if err != nil {
			t.Fatalf("List(%q): %v", tt.prefix, err)
		}
		if len(keys) == 0 {
			keys = nil
		}
		// sort for deterministic comparison
		sort.Strings(keys)
		want := tt.want
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("List(%q) = %v, want %v", tt.prefix, keys, want)
		}
	}
}

func TestListSymlinkEscape_regressions(t *testing.T) {
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

	_ = s.Set(ctx, "legit", []byte("ok"))

	// symlink to file outside store
	if err := os.Symlink(
		filepath.Join(outside, "leaked"),
		filepath.Join(dir, "escape_file"),
	); err != nil {
		t.Fatal(err)
	}

	// symlink to directory outside store
	if err := os.Symlink(outside, filepath.Join(dir, "escape_dir")); err != nil {
		t.Fatal(err)
	}

	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "escape_file" || k == "escape_dir/leaked" {
			t.Fatalf("List surfaced symlinked key %q", k)
		}
	}
	if len(keys) != 1 || keys[0] != "legit" {
		t.Fatalf("got %v, want [legit]", keys)
	}
}

func TestNewTrailingSlash_regressions(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir+"/", nil)
	ctx := context.Background()

	if err := s.Set(ctx, "key", []byte("val")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "val" {
		t.Fatalf("got %q, want %q", got.Value, "val")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s := file.New(dir, nil)
	ctx := context.Background()

	_ = s.Set(ctx, "perm_test", []byte("val"))
	info, err := os.Stat(filepath.Join(dir, "perm_test"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 0600", perm)
	}
}
