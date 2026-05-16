package secret

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Mint generates a hex-encoded random token of n bytes, stores it under key
// in the given MutableStore, and returns the hex string.
//
// Mint exists so callers (notably the kit reference implementation) don't
// reach for crypto/rand directly when they need a one-shot bearer token —
// the canonical pattern is "ask the secret store, get a token, store it".
// Backends remain free to swap in stronger sources as they become available
// without every call site changing.
//
// n must be > 0; the returned token is 2*n hex characters.
func Mint(ctx context.Context, store MutableStore, key string, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("secret: mint size must be > 0, got %d", n)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("secret: mint random: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := store.Set(ctx, key, []byte(tok)); err != nil {
		return "", fmt.Errorf("secret: mint store: %w", err)
	}
	return tok, nil
}
