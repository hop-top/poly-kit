package breaker_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

// newCaptureLogger returns a charm/log logger that writes JSON-encoded
// records into the returned buffer at DebugLevel. Tests assert against
// the JSON output to keep checks robust against text-formatter style
// changes (colors, prefixes).
func newCaptureLogger() (*charmlog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := charmlog.NewWithOptions(buf, charmlog.Options{
		Level:     charmlog.DebugLevel,
		Formatter: charmlog.JSONFormatter,
	})
	return l, buf
}

func TestLogger_TripEmitsErrorEvent(t *testing.T) {
	const name = "test-log-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	lg, buf := newCaptureLogger()
	b := breaker.New(name, breaker.Logger(lg))

	b.Trip("manual-test")

	out := buf.String()
	require.NotEmpty(t, out, "expected at least one log line")
	assert.Contains(t, out, `"breaker.name":"test-log-trip"`)
	assert.Contains(t, out, `"breaker.reason":"manual-test"`)
	assert.Contains(t, out, `"level":"error"`)
}

func TestLogger_ResetEmitsInfoEvent(t *testing.T) {
	const name = "test-log-reset"
	t.Cleanup(func() { breaker.Unregister(name) })

	lg, buf := newCaptureLogger()
	b := breaker.New(name, breaker.Logger(lg))
	b.Trip("setup")
	buf.Reset()

	b.Reset()

	out := buf.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, `"breaker.name":"test-log-reset"`)
	assert.Contains(t, out, `"level":"info"`)
}

// TestLogger_DefaultUsesKitLog asserts that without an injected logger,
// the breaker falls back to kitlog.New(viper.GetViper()) — its output
// goes to os.Stderr by default. We can't easily capture that here
// without redirecting fds; the contract is exercised by ensuring
// construction + Trip don't panic.
func TestLogger_DefaultUsesKitLog(t *testing.T) {
	const name = "test-log-default"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	assert.NotPanics(t, func() { b.Trip("default-test") })
}

func TestLogger_CircuitTransitionsLogged(t *testing.T) {
	const name = "test-log-transitions"
	t.Cleanup(func() { breaker.Unregister(name) })

	lg, buf := newCaptureLogger()
	b := breaker.New(name,
		breaker.Logger(lg),
		breaker.WithCircuit(breaker.CircuitOpts{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			Delay:            5 * time.Millisecond,
		}),
	)

	// trigger Closed -> Open via the circuit's failure path
	b.Record(false, 0)
	out := buf.String()
	assert.Contains(t, out, `"breaker.state":"open"`)
}

func TestLogger_OnTripWarn_LogsButDoesNotBlock(t *testing.T) {
	const name = "test-log-warn"
	t.Cleanup(func() { breaker.Unregister(name) })

	lg, buf := newCaptureLogger()
	_ = context.TODO()
	b := breaker.New(name,
		breaker.OnTrip(breaker.Warn),
		breaker.Logger(lg),
	)
	b.Trip("warn-test")

	// trip event should still log
	assert.Contains(t, buf.String(), "warn-test")
}

// TestLogger_BufferCaptureContract is the new ADR-0007 acceptance test:
// WithLogger(charmlog.NewWithOptions(buf, …)) captures transition
// messages and the buffer contents match expected log lines.
func TestLogger_BufferCaptureContract(t *testing.T) {
	const name = "test-log-buf-contract"
	t.Cleanup(func() { breaker.Unregister(name) })

	buf := &bytes.Buffer{}
	lg := charmlog.NewWithOptions(buf, charmlog.Options{
		Level:     charmlog.InfoLevel,
		Formatter: charmlog.JSONFormatter,
	})

	b := breaker.New(name,
		breaker.Logger(lg),
		breaker.WithCircuit(breaker.CircuitOpts{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			Delay:            5 * time.Millisecond,
		}),
	)

	b.Trip("contract-trip")
	b.Reset()

	out := buf.String()
	// manual trip = ERROR, with the supplied reason
	assert.Contains(t, out, `"level":"error"`)
	assert.Contains(t, out, `"breaker.reason":"contract-trip"`)
	// reset = INFO
	assert.Contains(t, out, `"level":"info"`)
	assert.Contains(t, out, `"msg":"breaker reset"`)
	// every line carries the breaker name
	assert.Contains(t, out, `"breaker.name":"test-log-buf-contract"`)
}
