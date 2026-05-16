package secret_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/agefile"
	"hop.top/kit/go/storage/secret/env"
	"hop.top/kit/go/storage/secret/memory"
)

// writeTestAgeYAML encrypts payload with a fresh X25519 identity and
// writes ciphertext + identity files under dir. Returned paths are
// passed to agefile.New.
func writeTestAgeYAML(t *testing.T, dir string, payload string) (cipherPath, identPath string) {
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

// memory backend always returns ErrNotSupported for Metadata.
func TestSecret_Metadata_Memory_ReturnsErrNotSupported(t *testing.T) {
	s := memory.New()

	var r secret.MetadataReader = s
	_, err := r.Metadata(context.Background(), "anything")
	if !errors.Is(err, secret.ErrNotSupported) {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("expected error to mention backend name; got %q", err)
	}
}

// env backend always returns ErrNotSupported for Metadata.
func TestSecret_Metadata_Env_ReturnsErrNotSupported(t *testing.T) {
	s := env.New("KIT_TEST_")

	var r secret.MetadataReader = s
	_, err := r.Metadata(context.Background(), "anything")
	if !errors.Is(err, secret.ErrNotSupported) {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "env") {
		t.Errorf("expected error to mention backend name; got %q", err)
	}
}

// agefile backend reports the encrypted file's mtime as UpdatedAt and
// composes Source from the file path.
func TestSecret_Metadata_Agefile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cipher, ident := writeTestAgeYAML(t, dir, "github_token: ghp_test\n")

	s := agefile.New(cipher, ident)
	var r secret.MetadataReader = s

	meta, err := r.Metadata(context.Background(), "github_token")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Key != "github_token" {
		t.Errorf("Key = %q, want github_token", meta.Key)
	}
	if meta.Backend != "agefile" {
		t.Errorf("Backend = %q, want agefile", meta.Backend)
	}
	if !strings.HasPrefix(meta.Source, "agefile/") {
		t.Errorf("Source = %q, want prefix agefile/", meta.Source)
	}
	if meta.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero; expected file mtime")
	}

	// Missing key returns ErrNotFound.
	_, err = r.Metadata(context.Background(), "nope")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("missing key: expected ErrNotFound, got %v", err)
	}
}

// StoredMeta marshals to JSON with all spec'd fields and the right
// omitempty tags; ExpiresAt = nil is omitted, Scopes = nil is omitted.
func TestSecret_StoredMeta_JSON_MarshalsWithoutSecretValue(t *testing.T) {
	exp := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	upd := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	meta := secret.StoredMeta{
		Key:        "github_token",
		ExpiresAt:  &exp,
		Scopes:     []string{"repo", "read:org"},
		Source:     "keyring/kit",
		AuthMethod: "bearer",
		Backend:    "keyring",
		UpdatedAt:  upd,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)

	wantSubstrings := []string{
		`"key":"github_token"`,
		`"expires_at":"2026-12-31T23:59:59Z"`,
		`"source":"keyring/kit"`,
		`"auth_method":"bearer"`,
		`"backend":"keyring"`,
		`"updated_at":"2026-05-01T12:00:00Z"`,
		`"scopes":["repo","read:org"]`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(got, sub) {
			t.Errorf("JSON missing %q: %s", sub, got)
		}
	}

	// Sanity: never serializes a "value" key.
	if strings.Contains(got, `"value"`) {
		t.Errorf("JSON unexpectedly contains \"value\" key (must NOT carry the secret): %s", got)
	}
}

// Empty StoredMeta omits optional fields.
func TestSecret_StoredMeta_JSON_OmitsZeroOptionals(t *testing.T) {
	meta := secret.StoredMeta{Key: "k", Source: "memory/test"}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, omitKey := range []string{"expires_at", "scopes", "auth_method", "backend"} {
		if strings.Contains(got, omitKey) {
			t.Errorf("expected %q omitted, got %s", omitKey, got)
		}
	}
}
