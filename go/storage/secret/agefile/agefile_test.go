package agefile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"hop.top/kit/go/storage/secret"
)

// writeAgeYAML encrypts a YAML payload with a fresh X25519 identity and
// writes both the ciphertext and the identity file to dir. Returns paths.
func writeAgeYAML(t *testing.T, dir string, payload string) (cipherPath, identPath string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}

	identPath = filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(identPath, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	cipherPath = filepath.Join(dir, "secrets.age")
	f, err := os.Create(cipherPath)
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}
	w, err := age.Encrypt(f, id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close encrypter: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return cipherPath, identPath
}

func TestGetReturnsValue(t *testing.T) {
	dir := t.TempDir()
	cipher, ident := writeAgeYAML(t, dir, "openai_api_key: sk-test-123\nstripe_secret: sk_live_xyz\n")

	s := New(cipher, ident)
	got, err := s.Get(context.Background(), "openai_api_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "sk-test-123" {
		t.Errorf("Get value: got %q, want %q", got.Value, "sk-test-123")
	}
}

func TestGetNotFoundReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	cipher, ident := writeAgeYAML(t, dir, "k1: v1\n")

	s := New(cipher, ident)
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListWithPrefix(t *testing.T) {
	dir := t.TempDir()
	cipher, ident := writeAgeYAML(t, dir, "ai_openai: a\nai_anthropic: b\ndb_password: c\n")

	s := New(cipher, ident)
	keys, err := s.List(context.Background(), "ai_")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("List ai_*: got %d keys, want 2 (%v)", len(keys), keys)
	}
}

func TestSetReturnsNotSupported(t *testing.T) {
	s := New("/nonexistent", "/nonexistent")
	err := s.Set(context.Background(), "k", []byte("v"))
	if !errors.Is(err, secret.ErrNotSupported) {
		t.Errorf("expected ErrNotSupported, got %v", err)
	}
}

func TestDeleteReturnsNotSupported(t *testing.T) {
	s := New("/nonexistent", "/nonexistent")
	err := s.Delete(context.Background(), "k")
	if !errors.Is(err, secret.ErrNotSupported) {
		t.Errorf("expected ErrNotSupported, got %v", err)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	cipher, ident := writeAgeYAML(t, dir, "present: yes\n")

	s := New(cipher, ident)
	ok, err := s.Exists(context.Background(), "present")
	if err != nil || !ok {
		t.Errorf("Exists(present): want true,nil; got %v,%v", ok, err)
	}
	ok, err = s.Exists(context.Background(), "absent")
	if err != nil || ok {
		t.Errorf("Exists(absent): want false,nil; got %v,%v", ok, err)
	}
}
