package secret_test

import (
	"testing"

	"hop.top/kit/go/storage/secret"
)

func TestOpenUnknownBackend(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestOpenRegisteredBackend(t *testing.T) {
	secret.RegisterBackend("test-mock", func(cfg secret.Config) (secret.MutableStore, error) {
		return nil, nil
	})
	_, err := secret.Open(secret.Config{Backend: "test-mock"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
