package secret_test

import (
	"testing"

	"hop.top/kit/go/storage/secret"
)

func TestErrNotFound(t *testing.T) {
	if secret.ErrNotFound == nil {
		t.Fatal("ErrNotFound should not be nil")
	}
	if secret.ErrNotFound.Error() != "secret: not found" {
		t.Fatalf("unexpected error message: %s", secret.ErrNotFound.Error())
	}
}
