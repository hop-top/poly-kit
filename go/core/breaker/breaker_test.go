package breaker_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

func TestNew_RequiresName(t *testing.T) {
	defer breaker.Unregister("")
	assert.Panics(t, func() { breaker.New("") })
}

func TestNew_RegistersBreaker(t *testing.T) {
	const name = "test-new-registers"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	require.NotNil(t, b)
	assert.Equal(t, name, b.Name())

	got, ok := breaker.Lookup(name)
	require.True(t, ok)
	assert.Same(t, b, got)
}

func TestNew_DoubleRegisterPanics(t *testing.T) {
	const name = "test-double-register"
	t.Cleanup(func() { breaker.Unregister(name) })

	breaker.New(name)
	assert.Panics(t, func() { breaker.New(name) })
}

func TestBreaker_AllowClosedReturnsNil(t *testing.T) {
	const name = "test-allow-closed"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	assert.NoError(t, b.Allow())
	assert.Equal(t, breaker.Closed, b.State())
}

func TestBreaker_TripOpensCircuit(t *testing.T) {
	const name = "test-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("manual")

	assert.Equal(t, breaker.Open, b.State())
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)

	stats := b.Stats()
	assert.Equal(t, uint64(1), stats.Trips)
	assert.Equal(t, "manual", stats.LastTripReason)
	assert.False(t, stats.LastTripAt.IsZero())
}

func TestBreaker_ResetClosesCircuit(t *testing.T) {
	const name = "test-reset"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("test")
	require.Equal(t, breaker.Open, b.State())

	b.Reset()
	assert.Equal(t, breaker.Closed, b.State())
	assert.NoError(t, b.Allow())
}

func TestBreaker_RecordUpdatesCounters(t *testing.T) {
	const name = "test-record"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Record(true, 100)
	b.Record(true, 50)
	b.Record(false, 25)

	stats := b.Stats()
	// Counters track lifetime byte total under the well-known "bytes" key
	// and op count under "ops".
	assert.Equal(t, int64(175), stats.Counters["bytes"])
	assert.Equal(t, int64(3), stats.Counters["ops"])
}

func TestErrors_AreSentinels(t *testing.T) {
	wrapped := errors.New("wrapping the breaker error")
	assert.NotErrorIs(t, wrapped, breaker.ErrBrokenCircuit)
	assert.ErrorIs(t, breaker.ErrBrokenCircuit, breaker.ErrBrokenCircuit)
	assert.ErrorIs(t, breaker.ErrThresholdExceeded, breaker.ErrThresholdExceeded)
}

func TestState_String(t *testing.T) {
	assert.Equal(t, "closed", breaker.Closed.String())
	assert.Equal(t, "open", breaker.Open.String())
	assert.Equal(t, "half_open", breaker.HalfOpen.String())
}

func TestAction_String(t *testing.T) {
	assert.Equal(t, "halt", breaker.Halt.String())
	assert.Equal(t, "degrade", breaker.Degrade.String())
	assert.Equal(t, "warn", breaker.Warn.String())
}
