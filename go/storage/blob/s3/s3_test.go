package s3

import (
	"testing"

	"hop.top/kit/go/storage/blob"
)

// Compile-time interface assertion — runs without build tags.
var _ blob.Store = (*Store)(nil)

func TestInterfaceCompliance(t *testing.T) {
	// Intentionally empty; the var _ line above is the real check.
	t.Log("Store implements blob.Store")
}
