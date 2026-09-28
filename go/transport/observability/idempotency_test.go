package observability

import (
	"fmt"
	"testing"

	"hop.top/kit/go/transport/cmdsurface"
)

// TestRefusalCodeIdempotency counts the idempotency refusals by their
// own codes, wrapped as the bridge returns them.
func TestRefusalCodeIdempotency(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("x: %w", cmdsurface.ErrIdempotencyConflict):  RefusalIdempotencyConflict,
		fmt.Errorf("x: %w", cmdsurface.ErrIdempotencyKeyReused): RefusalIdempotencyKeyReused,
	} {
		if got := RefusalCode(err); got != want {
			t.Errorf("RefusalCode(%v) = %q, want %q", err, got, want)
		}
	}
}
