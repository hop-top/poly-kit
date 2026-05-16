package secret_test

import (
	"context"
	"testing"

	"hop.top/kit/go/storage/secret"
	_ "hop.top/kit/go/storage/secret/memory"
)

func TestMint(t *testing.T) {
	t.Parallel()

	store, err := secret.Open(secret.Config{Backend: "memory", Service: "test"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()

	tok, err := secret.Mint(ctx, store, "auth", 16)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(tok) != 32 {
		t.Fatalf("expected 32 hex chars (16 bytes), got %d", len(tok))
	}

	got, err := store.Get(ctx, "auth")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Value) != tok {
		t.Fatalf("stored token mismatch: %q vs %q", got.Value, tok)
	}

	tok2, err := secret.Mint(ctx, store, "auth2", 16)
	if err != nil {
		t.Fatalf("mint2: %v", err)
	}
	if tok2 == tok {
		t.Fatal("two mints returned same token; not random")
	}
}

func TestMintInvalidSize(t *testing.T) {
	t.Parallel()

	store, err := secret.Open(secret.Config{Backend: "memory", Service: "test"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := secret.Mint(context.Background(), store, "k", 0); err == nil {
		t.Fatal("expected error for n=0")
	}
}
