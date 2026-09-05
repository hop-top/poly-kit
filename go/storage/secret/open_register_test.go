package secret_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
)

func TestOpenFileMissingDir(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "file"})
	if err == nil {
		t.Fatal("expected error for missing Dir")
	}
}

// TestOpenFileRoundtrip proves the store Open hands back is the real
// file backend writing under Config.Dir, not merely non-nil.
func TestOpenFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := secret.Open(secret.Config{Backend: "file", Dir: dir})
	if err != nil {
		t.Fatalf("Open file: %v", err)
	}
	ctx := context.Background()
	if err := s.Set(ctx, "db_pass", []byte("s3cr3t")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "db_pass")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "s3cr3t" {
		t.Fatalf("got %q, want %q", got.Value, "s3cr3t")
	}
	// Written under the configured directory, in plaintext: no keeper
	// is expressible through Config.
	raw, err := os.ReadFile(filepath.Join(dir, "db_pass"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != "s3cr3t" {
		t.Fatalf("on-disk %q, want plaintext %q", raw, "s3cr3t")
	}
}

func TestOpenFileMissingKeyIsNotFound(t *testing.T) {
	s, err := secret.Open(secret.Config{Backend: "file", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open file: %v", err)
	}
	if _, err := s.Get(context.Background(), "absent"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestOpenOpenbaoMissingAddr(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "openbao", Token: "tok"})
	if err == nil {
		t.Fatal("expected error for missing Addr")
	}
}

// TestOpenOpenbaoMalformedAddr covers the one failure openbao can
// detect without touching the network: an unparseable address.
func TestOpenOpenbaoMalformedAddr(t *testing.T) {
	_, err := secret.Open(secret.Config{
		Backend: "openbao", Addr: "://not a url", Token: "tok",
	})
	if err == nil {
		t.Fatal("expected error for malformed Addr")
	}
}

// TestOpenOpenbaoUnreachableOpensLazily pins the chosen failure mode:
// Open performs no network I/O, so a server that is down still yields
// a store and the error surfaces on first use. This matches the other
// network-backed backends (infisical, onepassword, ghsecrets), none of
// which dial in their constructors.
func TestOpenOpenbaoUnreachableOpensLazily(t *testing.T) {
	s, err := secret.Open(secret.Config{
		// Port 1 refuses connections; nothing is listening.
		Backend: "openbao", Addr: "http://127.0.0.1:1", Token: "tok",
	})
	if err != nil {
		t.Fatalf("Open must not dial: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	// Bound the attempt: the client retries, and the point here is that
	// the error arrives at first use rather than at Open, not how long
	// the transport spends trying.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.Get(ctx, "k"); err == nil {
		t.Fatal("expected first use against unreachable server to fail")
	}
}

// TestOpenOpenbaoDefaultMount documents that an omitted Mount is
// accepted; the backend defaults it rather than rejecting the config.
func TestOpenOpenbaoDefaultMount(t *testing.T) {
	s, err := secret.Open(secret.Config{
		Backend: "openbao", Addr: "http://127.0.0.1:8200", Token: "tok",
	})
	if err != nil {
		t.Fatalf("Open openbao without Mount: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

// TestOpenEnvIsReadOnly pins the shim precedent the new registrations
// follow: a read-only backend still satisfies MutableStore and errors
// on write rather than being excluded from Open.
func TestOpenEnvIsReadOnly(t *testing.T) {
	s, err := secret.Open(secret.Config{Backend: "env", Prefix: "KIT_"})
	if err != nil {
		t.Fatalf("Open env: %v", err)
	}
	if err := s.Set(context.Background(), "k", []byte("v")); !errors.Is(err, secret.ErrNotSupported) {
		t.Fatalf("got %v, want ErrNotSupported", err)
	}
}
