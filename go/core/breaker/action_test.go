package breaker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

func TestOnTripWarn_AllowsAndLogs(t *testing.T) {
	lg, buf := newCaptureLogger()
	b, _ := newClocked(t, "act-warn", 1, 1, breaker.OnTrip(breaker.Warn), breaker.Logger(lg))
	b.Trip("ops")
	assert.NoError(t, b.Allow())
	assert.Contains(t, buf.String(), `{"level":"warn","msg":"breaker would reject","breaker.name":"act-warn","breaker.state":"open","breaker.policy":"circuit","breaker.action":"warn"}`)
}

func TestOnTripWarn_NamesTheRejectingPolicy(t *testing.T) {
	lg, buf := newCaptureLogger()
	b, _ := newClocked(t, "act-warn-ops", 1, 1, breaker.OnTrip(breaker.Warn), breaker.Logger(lg), breaker.MaxOps(1))
	b.Record(true, 0)
	assert.NoError(t, b.Allow())
	assert.Contains(t, buf.String(), `"breaker.state":"closed","breaker.policy":"ops","breaker.action":"warn"`)
}

func TestOnTripHalt_IgnoresFallback(t *testing.T) {
	called := false
	b, _ := newClocked(t, "act-halt-fb", 1, 1, breaker.Fallback(func(context.Context) error {
		called = true
		return nil
	}))
	b.Trip("ops")
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
	assert.False(t, called, "Halt fails closed")

	err := b.Executor().Run(func() error { return nil })
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
	assert.False(t, called, "Halt: Executor installs no fallback")
}

func TestOnTripDegrade_RoutesToFallback(t *testing.T) {
	calls := 0
	b, _ := newClocked(t, "act-degrade", 1, 1, breaker.OnTrip(breaker.Degrade), breaker.Fallback(func(context.Context) error {
		calls++
		return nil
	}))
	b.Trip("ops")
	assert.NoError(t, b.Allow())
	assert.NoError(t, b.Executor().Run(func() error { return nil }))
	assert.Equal(t, 2, calls)
}

func TestOnTripDegrade_FailingFallbackBreaks(t *testing.T) {
	b, _ := newClocked(t, "act-degrade-err", 1, 1, breaker.OnTrip(breaker.Degrade), breaker.Fallback(func(context.Context) error {
		return errors.New("no alternate")
	}))
	b.Trip("ops")
	assert.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}

func TestOnTripDegrade_WithoutFallbackHalts(t *testing.T) {
	b, _ := newClocked(t, "act-degrade-nofb", 1, 1, breaker.OnTrip(breaker.Degrade))
	b.Trip("ops")
	require.ErrorIs(t, b.Allow(), breaker.ErrBrokenCircuit)
}
