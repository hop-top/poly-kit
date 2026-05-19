package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// grantHook is a permissive ConsentHook for tests; lives only in
// kit-telemetry tests until kit-consent ships the real implementation.
type grantHook struct{}

func (grantHook) Granted(context.Context) bool { return true }

func TestDefaultHookDenies(t *testing.T) {
	// Don't mutate globals; the package init seeds denyHook{} and
	// every other test that touches the global resets via t.Cleanup.
	assert.False(t, CurrentConsentHook().Granted(context.Background()))
}

func TestSetConsentHook_Grants(t *testing.T) {
	t.Cleanup(func() { SetConsentHook(nil) })
	SetConsentHook(grantHook{})
	assert.True(t, CurrentConsentHook().Granted(context.Background()))
}

func TestSetConsentHook_NilResetsToDefault(t *testing.T) {
	t.Cleanup(func() { SetConsentHook(nil) })
	SetConsentHook(grantHook{})
	assert.True(t, CurrentConsentHook().Granted(context.Background()))
	SetConsentHook(nil)
	assert.False(t, CurrentConsentHook().Granted(context.Background()),
		"SetConsentHook(nil) must restore default-deny, not store nil")
}

func TestWithConsentHook(t *testing.T) {
	// Global stays default-deny; only the ctx-scoped hook grants.
	ctx := WithConsentHook(context.Background(), grantHook{})
	assert.True(t, CurrentConsentHookFromContext(ctx).Granted(ctx))
	// Global state must be unchanged by the per-ctx override.
	assert.False(t, CurrentConsentHook().Granted(context.Background()))
}

func TestCurrentConsentHookFromContext_NilCtx(t *testing.T) {
	// Nil ctx falls back to the global (default-deny here).
	//nolint:staticcheck // intentionally exercising nil-ctx safety
	got := CurrentConsentHookFromContext(nil)
	assert.NotNil(t, got)
	assert.False(t, got.Granted(context.Background()))
}

func TestCurrentConsentHookFromContext_EmptyCtx(t *testing.T) {
	// Background ctx with no override also falls back to global.
	got := CurrentConsentHookFromContext(context.Background())
	assert.NotNil(t, got)
	assert.False(t, got.Granted(context.Background()))
}

func TestConcurrent_HookSwap(t *testing.T) {
	t.Cleanup(func() { SetConsentHook(nil) })

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		if i%2 == 0 {
			// reader: must always get a non-nil hook back; race
			// detector catches torn reads / data races on the
			// atomic.Value slot.
			go func() {
				defer wg.Done()
				h := CurrentConsentHook()
				assert.NotNil(t, h)
				_ = h.Granted(context.Background())
			}()
		} else {
			// writer: alternate between permissive + reset-to-default.
			go func(idx int) {
				defer wg.Done()
				if idx%4 == 1 {
					SetConsentHook(grantHook{})
				} else {
					SetConsentHook(nil)
				}
			}(i)
		}
	}
	wg.Wait()
}
