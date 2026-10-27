package breaker_test

import (
	"sync"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

// fakeClock is a manually advanced breaker.Clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestWithClock_CooldownFollowsClock(t *testing.T) {
	const name = "test-clock-cooldown"
	t.Cleanup(func() { breaker.Unregister(name) })

	clk := newFakeClock()
	b := breaker.New(name, breaker.WithClock(clk), breaker.WithCircuit(breaker.CircuitOpts{
		FailureThreshold: 1,
		Delay:            10 * time.Second,
	}))
	b.Record(false, 0)
	require.Equal(t, breaker.Open, b.State())

	clk.Advance(10*time.Second - time.Nanosecond)
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit, "cooldown not yet elapsed")

	clk.Advance(time.Nanosecond)
	assert.NoError(t, b.Allow(), "cooldown elapsed: a probe is admitted")
}

func TestWithClock_StampsManualTrip(t *testing.T) {
	const name = "test-clock-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	clk := newFakeClock()
	b := breaker.New(name, breaker.WithClock(clk))
	clk.Advance(time.Minute)
	b.Trip("ops")
	assert.Equal(t, clk.Now(), b.Stats().LastTripAt)
}

func TestExecutor_OpenCircuitMatchesBothSentinels(t *testing.T) {
	const name = "test-executor-open"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("ops")
	err := b.Executor().Run(func() error { return nil })
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
	assert.ErrorIs(t, err, circuitbreaker.ErrOpen)
}
