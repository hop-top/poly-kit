package breaker_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

var errBoom = errors.New("boom")

// newClocked builds a breaker on a fake clock with FailureThreshold f,
// SuccessThreshold s and a 10s cooldown, unregistered on cleanup.
func newClocked(t *testing.T, name string, f, s uint, opts ...breaker.Option) (breaker.Breaker, *fakeClock) {
	t.Helper()
	t.Cleanup(func() { breaker.Unregister(name) })
	clk := newFakeClock()
	opts = append([]breaker.Option{
		breaker.WithClock(clk),
		breaker.WithCircuit(breaker.CircuitOpts{FailureThreshold: f, SuccessThreshold: s, Delay: 10 * time.Second}),
	}, opts...)
	return breaker.New(name, opts...), clk
}

func TestWrapErr_FailuresTripAtThreshold(t *testing.T) {
	b, _ := newClocked(t, "reg-wrap-trips", 3, 1)
	for i := 0; i < 3; i++ {
		require.ErrorIs(t, breaker.WrapErr(b, func() error { return errBoom }), errBoom)
	}
	assert.Equal(t, breaker.Open, b.State())

	called := false
	err := breaker.WrapErr(b, func() error { called = true; return nil })
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
	assert.False(t, called, "open circuit must short-circuit fn")
}

func TestAllow_IsNotASuccess(t *testing.T) {
	b, _ := newClocked(t, "reg-allow-noop", 3, 1)
	b.Record(false, 0)
	b.Record(false, 0)
	require.NoError(t, b.Allow())
	b.Record(false, 0)
	assert.Equal(t, breaker.Open, b.State(), "Allow between failures must not reset the window")
}

func TestState_ReflectsCooldownExpiry(t *testing.T) {
	lg, buf := newCaptureLogger()
	b, clk := newClocked(t, "reg-state-cooldown", 1, 1, breaker.Logger(lg))
	b.Record(false, 0)
	clk.Advance(10*time.Second - time.Nanosecond)
	assert.Equal(t, breaker.Open, b.State())
	clk.Advance(time.Nanosecond)
	assert.Equal(t, breaker.HalfOpen, b.State(), "expiry visible without Allow")
	assert.Contains(t, buf.String(), `"breaker.state":"half_open","breaker.prev_state":"open"`)
}

// Also green before the fix (Allow closed, the failure re-opened from
// Closed); guards the probe-failure direction.
func TestHalfOpen_FailingProbeReopens(t *testing.T) {
	b, clk := newClocked(t, "reg-probe-fail", 1, 1)
	b.Record(false, 0)
	clk.Advance(10 * time.Second)
	require.ErrorIs(t, breaker.WrapErr(b, func() error { return errBoom }), errBoom)
	assert.Equal(t, breaker.Open, b.State())
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit, "re-opened circuit restarts its cooldown")
}

func TestHalfOpen_SuccessfulProbeCloses(t *testing.T) {
	b, clk := newClocked(t, "reg-probe-ok", 1, 2)
	b.Record(false, 0)
	clk.Advance(10 * time.Second)
	require.NoError(t, breaker.WrapErr(b, func() error { return nil }))
	assert.Equal(t, breaker.HalfOpen, b.State(), "one of two required successes")
	require.NoError(t, breaker.WrapErr(b, func() error { return nil }))
	assert.Equal(t, breaker.Closed, b.State())
}

func TestHalfOpen_PermitsBoundInFlightProbes(t *testing.T) {
	b, clk := newClocked(t, "reg-probe-permits", 1, 1)
	b.Record(false, 0)
	clk.Advance(10 * time.Second)
	require.NoError(t, b.Allow(), "first probe admitted")
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit, "second probe waits for the first")
	assert.Equal(t, breaker.HalfOpen, b.State())
	b.Record(true, 0)
	assert.Equal(t, breaker.Closed, b.State())
}

// The rate limiter runs on failsafe's wall clock, hence the real sleep;
// the 200ms period leaves the first two Allows well inside one period.
func TestHalfOpen_PreflightRejectionReleasesPermit(t *testing.T) {
	b, clk := newClocked(t, "reg-probe-release", 1, 2, breaker.MaxPerInterval(1, 200*time.Millisecond))
	b.Record(false, 0)
	clk.Advance(10 * time.Second)
	require.NoError(t, b.Allow(), "probe 1 of 2: circuit and rate admit")
	require.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit, "probe 2: circuit admits, rate rejects")
	time.Sleep(250 * time.Millisecond)
	assert.NoError(t, b.Allow(), "the rate-rejected probe gave its permit back")
}

func TestAllow_PreflightRejectionsDoNotFeedCircuit(t *testing.T) {
	b, _ := newClocked(t, "reg-preflight", 2, 1, breaker.MaxOps(1))
	b.Record(true, 0)
	for i := 0; i < 5; i++ {
		require.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
	}
	assert.Equal(t, breaker.Closed, b.State(), "budget rejections are not downstream failures")
}

func TestTripsTotal_CountsAutomaticOpens(t *testing.T) {
	lg, buf := newCaptureLogger()
	b, clk := newClocked(t, "reg-trips-auto", 1, 1, breaker.Logger(lg))
	clk.Advance(time.Minute)
	b.Record(false, 0) // closed -> open
	st := b.Stats()
	assert.Equal(t, uint64(1), st.Trips)
	assert.Equal(t, clk.Now(), st.LastTripAt)
	assert.Equal(t, "circuit", st.LastTripReason)
	assert.Contains(t, buf.String(), `"breaker.prev_state":"closed","breaker.policy":"circuit","breaker.trips_total":1`)

	clk.Advance(10 * time.Second)
	b.Record(false, 0) // half_open -> open
	assert.Equal(t, uint64(2), b.Stats().Trips)
}

func TestTripsTotal_ManualTripCountsOnce(t *testing.T) {
	b, _ := newClocked(t, "reg-trips-manual", 1, 1)
	b.Trip("ops")
	st := b.Stats()
	assert.Equal(t, uint64(1), st.Trips, "the manual open is not also counted as automatic")
	assert.Equal(t, "ops", st.LastTripReason)
}

func TestReset_ClearsFailureWindowWhenClosed(t *testing.T) {
	b, _ := newClocked(t, "reg-reset-window", 3, 1)
	b.Record(false, 0)
	b.Record(false, 0)
	b.Reset()
	b.Record(false, 0)
	assert.Equal(t, breaker.Closed, b.State(), "Reset starts a fresh window")
	b.Record(false, 0)
	b.Record(false, 0)
	assert.Equal(t, breaker.Open, b.State())
}

func TestReset_ClosesFromHalfOpen(t *testing.T) {
	b, clk := newClocked(t, "reg-reset-half", 1, 1)
	b.Record(false, 0)
	clk.Advance(10 * time.Second)
	require.NoError(t, b.Allow())
	b.Reset()
	assert.Equal(t, breaker.Closed, b.State())
	require.NoError(t, b.Allow())
	require.NoError(t, b.Allow(), "closed: no probe limit")
}

// Guard, green before this change too: Trip reads State() first.
func TestTrip_AfterExpiredCooldownRestartsIt(t *testing.T) {
	b, clk := newClocked(t, "reg-trip-expired", 1, 1)
	b.Trip("first")
	clk.Advance(10 * time.Second)
	b.Trip("second")
	assert.Equal(t, breaker.Open, b.State(), "a trip holds for a full cooldown")
	clk.Advance(10 * time.Second)
	assert.Equal(t, breaker.HalfOpen, b.State())
}

func TestTrip_ExpiryTransitionPrecedesTheCount(t *testing.T) {
	lg, buf := newCaptureLogger()
	b, clk := newClocked(t, "reg-trip-order", 1, 1, breaker.Logger(lg))
	b.Trip("first")
	clk.Advance(10 * time.Second)
	buf.Reset()
	b.Trip("second")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], `"breaker.state":"half_open","breaker.prev_state":"open","breaker.policy":"circuit","breaker.trips_total":1`)
	assert.Contains(t, lines[1], `"breaker tripped manually"`)
	assert.Contains(t, lines[1], `"breaker.trips_total":2`)
	assert.Contains(t, lines[2], `"breaker.state":"open","breaker.prev_state":"half_open"`)
}
