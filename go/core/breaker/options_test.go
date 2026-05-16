package breaker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

func TestMaxPerInterval_AllowsThenBlocks(t *testing.T) {
	const name = "test-rate"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.MaxPerInterval(2, time.Second))
	assert.NoError(t, b.Allow())
	assert.NoError(t, b.Allow())
	// third call within the second should short-circuit
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestMaxPerMinute_DelegatesToInterval(t *testing.T) {
	const name = "test-rate-minute"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.MaxPerMinute(1))
	assert.NoError(t, b.Allow())
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestMaxConcurrent_NoBlockingOnAllowSequential(t *testing.T) {
	const name = "test-concurrent"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.MaxConcurrent(1))
	assert.NoError(t, b.Allow())
	assert.NoError(t, b.Allow())
}

func TestMaxBytes_BlocksAtThreshold(t *testing.T) {
	const name = "test-max-bytes"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.MaxBytes(100))
	// fresh breaker: counter is 0, Allow ok
	require.NoError(t, b.Allow())

	// record 100 bytes; counter is now at threshold
	b.Record(true, 100)
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestMaxOps_BlocksAtThreshold(t *testing.T) {
	const name = "test-max-ops"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.MaxOps(2))
	require.NoError(t, b.Allow())
	b.Record(true, 0)
	require.NoError(t, b.Allow())
	b.Record(true, 0)
	// counter == 2, at threshold
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestTimeout_AppliesToExecutor(t *testing.T) {
	const name = "test-timeout"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.Timeout(10*time.Millisecond))
	// Allow's noop fn returns immediately; just confirm composition
	// works (no panic, no error from instant return).
	assert.NoError(t, b.Allow())

	// Direct executor exposes the timeout for slow fn.
	err := b.Executor().Run(func() error {
		select {
		case <-time.After(50 * time.Millisecond):
			return nil
		case <-context.Background().Done():
			return context.Canceled
		}
	})
	require.Error(t, err)
}

func TestWithCircuit_OverridesDefaults(t *testing.T) {
	const name = "test-with-circuit"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name, breaker.WithCircuit(breaker.CircuitOpts{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Delay:            10 * time.Millisecond,
	}))
	// trip after a single failure
	b.Record(false, 0)
	assert.Equal(t, breaker.Open, b.State())
}

func TestResetAfter_SetsCircuitDelay(t *testing.T) {
	const name = "test-reset-after"
	t.Cleanup(func() { breaker.Unregister(name) })

	// Just confirms the option is accepted and breaker constructs.
	b := breaker.New(name, breaker.ResetAfter(50*time.Millisecond))
	assert.NotNil(t, b)
}

func TestFallback_RoutesAllowedThrough(t *testing.T) {
	const name = "test-fallback"
	t.Cleanup(func() { breaker.Unregister(name) })

	called := false
	fb := func(ctx context.Context) error {
		called = true
		return nil
	}
	b := breaker.New(name,
		breaker.OnTrip(breaker.Degrade),
		breaker.Fallback(fb),
	)
	b.Trip("manual")
	// With a fallback configured, Allow should succeed (fallback runs)
	err := b.Allow()
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestCombination_BytesPlusRate(t *testing.T) {
	const name = "test-combo"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name,
		breaker.MaxBytes(50),
		breaker.MaxPerInterval(10, time.Second),
	)
	require.NoError(t, b.Allow())
	b.Record(true, 50)
	// bytes threshold reached
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestErrors_Composition(t *testing.T) {
	// Cross-check sentinel chain: external err wrapping shouldn't
	// silently match.
	w := errors.New("not the breaker error")
	assert.NotErrorIs(t, w, breaker.ErrBrokenCircuit)
}
