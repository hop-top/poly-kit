package redact_test

import (
	"bytes"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/core/redact"
)

// TestWithLogger_CapturesCustomPanicWarning wires a kit/log logger
// pointed at a buffer through WithLogger, triggers the custom-formatter
// panic-recovery path, and asserts the warning landed in the buffer
// (not on the global slog default). Proves the migration actually
// routes redact's internal warnings through the injected logger.
func TestWithLogger_CapturesCustomPanicWarning(t *testing.T) {
	v := viper.New()
	v.Set("no-color", true) // keep buffer free of ANSI escapes

	logger := kitlog.New(v)
	var buf bytes.Buffer
	logger.SetOutput(&buf)

	r, err := redact.New(redact.WithLogger(logger)).
		AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Custom, func(_ redact.Match) string {
		panic("intentional")
	})
	require.NoError(t, err)

	// Trigger the panic-recovery path. Output must still mask correctly.
	out := r.Apply("a 12 b")
	assert.Equal(t, "a ***REDACTED*** b", out)

	// And the warning must have landed on the injected logger's writer.
	logged := buf.String()
	assert.Contains(t, logged, "redact: custom formatter panicked")
	assert.Contains(t, logged, "rule")
	assert.Contains(t, logged, "digits")
	assert.Contains(t, logged, "panic")
}

// TestWithLogger_NilLoggerKeepsDefault guards against a caller passing
// a nil *log.Logger and silently breaking the panic-recovery warning
// path. WithLogger ignores nil; the redactor keeps its viper-aware
// default and the panic-recovery still completes (no nil-deref).
func TestWithLogger_NilLoggerKeepsDefault(t *testing.T) {
	r, err := redact.New(redact.WithLogger(nil)).
		AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Custom, func(_ redact.Match) string {
		panic("intentional")
	})
	require.NoError(t, err)

	// Must not crash — default logger is in place.
	assert.NotPanics(t, func() {
		_ = r.Apply("a 12 b")
	})
}

// TestWithLogger_RespectsQuietLevel proves the injected kit/log logger
// honors the viper "quiet" key — i.e. the warning still fires (Warn
// is at/above the quiet floor of WarnLevel) and lands in the buffer.
// Catches the regression where a future refactor might filter warnings
// out under quiet.
func TestWithLogger_RespectsQuietLevel(t *testing.T) {
	v := viper.New()
	v.Set("quiet", true)
	v.Set("no-color", true)

	logger := kitlog.New(v)
	require.Equal(t, charmlog.WarnLevel, logger.GetLevel(),
		"quiet should clamp the level to Warn")

	var buf bytes.Buffer
	logger.SetOutput(&buf)

	r, err := redact.New(redact.WithLogger(logger)).
		AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Custom, func(_ redact.Match) string {
		panic("intentional")
	})
	require.NoError(t, err)

	_ = r.Apply("x 7 y")
	assert.Contains(t, buf.String(), "redact: custom formatter panicked",
		"warn-level message should pass the quiet floor")
}
