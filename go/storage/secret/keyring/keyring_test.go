//go:build !ci

package keyring_test

import (
	"context"
	"testing"

	"hop.top/kit/go/storage/secret"
	kr "hop.top/kit/go/storage/secret/keyring"
)

// Compile-time interface assertions.
var (
	_ secret.Store        = (*kr.Store)(nil)
	_ secret.MutableStore = (*kr.Store)(nil)
)

func TestListNotSupported(t *testing.T) {
	s := kr.New("hop-kit-test")
	_, err := s.List(context.Background(), "")
	if err != secret.ErrNotSupported {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
}

func TestRoundtrip(t *testing.T) {
	s := kr.New("hop-kit-test")
	ctx := context.Background()
	key := "test-roundtrip-key"

	// cleanup
	defer func() { _ = s.Delete(ctx, key) }()

	if err := s.Set(ctx, key, []byte("secret-value")); err != nil {
		t.Skipf("keyring unavailable: %v", err)
	}

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "secret-value" {
		t.Fatalf("got %q, want %q", got.Value, "secret-value")
	}

	ok, err := s.Exists(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected key to exist")
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}

	ok, err = s.Exists(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected key to be gone")
	}
}
