package local_test

import (
	"bytes"
	"context"
	"testing"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/storage/secret/local"
)

func TestRoundtrip(t *testing.T) {
	ctx := context.Background()
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	k := local.NewKeeper(kp)
	plaintext := []byte("super-secret-api-key")

	ciphertext, err := k.Encrypt(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	got, err := k.Decrypt(ctx, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestWrongKeyFails(t *testing.T) {
	ctx := context.Background()
	kp1, _ := identity.Generate()
	kp2, _ := identity.Generate()

	k1 := local.NewKeeper(kp1)
	k2 := local.NewKeeper(kp2)

	ciphertext, err := k1.Encrypt(ctx, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = k2.Decrypt(ctx, ciphertext)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong key")
	}
}

func TestEmptyPlaintext(t *testing.T) {
	ctx := context.Background()
	kp, _ := identity.Generate()
	k := local.NewKeeper(kp)

	ciphertext, err := k.Encrypt(ctx, []byte{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := k.Decrypt(ctx, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %q", got)
	}
}
