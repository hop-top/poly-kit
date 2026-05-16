//go:build !ci

package keyring_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hop.top/kit/go/storage/secret"
	kr "hop.top/kit/go/storage/secret/keyring"
)

// Compile-time assertion that *kr.Store satisfies MetadataReader.
var _ secret.MetadataReader = (*kr.Store)(nil)

func TestSecret_Metadata_Keyring_RoundTrip(t *testing.T) {
	s := kr.New("hop-kit-test")
	ctx := context.Background()
	key := "test-metadata-key"

	defer func() { _ = s.Delete(ctx, key) }()

	if err := s.Set(ctx, key, []byte("secret-value")); err != nil {
		t.Skipf("keyring unavailable: %v", err)
	}

	meta, err := s.Metadata(ctx, key)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Key != key {
		t.Errorf("Key = %q, want %q", meta.Key, key)
	}
	if meta.Backend != "keyring" {
		t.Errorf("Backend = %q, want keyring", meta.Backend)
	}
	if !strings.HasPrefix(meta.Source, "keyring/") {
		t.Errorf("Source = %q, want prefix keyring/", meta.Source)
	}

	// Missing key returns ErrNotFound.
	_, err = s.Metadata(ctx, "definitely-not-there-"+key)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("missing key: expected ErrNotFound, got %v", err)
	}
}
