package scope_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

// captureLogger returns a charm/log logger that writes to buf at
// WarnLevel. Color is disabled when the writer is not a TTY.
func captureLogger(buf *bytes.Buffer) *log.Logger {
	return log.NewWithOptions(buf, log.Options{Level: log.WarnLevel})
}

func TestEnforce_WarnRoutesViaInjectedLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := captureLogger(buf)

	p := scope.New(scope.WithLogger(logger)).
		SetMode(scope.Warn).
		Deny("/tmp/**")

	err := p.Enforce("/tmp/secret", scope.Read)
	require.NoError(t, err, "Warn mode swallows the error")

	out := buf.String()
	assert.Contains(t, out, "scope: path denied (warn mode, allowing)")
	assert.Contains(t, out, "/tmp/secret")
	assert.Contains(t, out, "read")
}

func TestWithLogger_NilIgnored(t *testing.T) {
	// Passing nil must not panic and must not nil out the default logger.
	p := scope.New(scope.WithLogger(nil)).SetMode(scope.Warn).Deny("/tmp/**")
	err := p.Enforce("/tmp/x", scope.Read)
	assert.NoError(t, err)
}

func TestEnforce_StrictReturnsErrDeniedNotLogged(t *testing.T) {
	// Sanity: Strict mode must not log via the injected logger when
	// denying — it returns ErrDenied instead.
	buf := &bytes.Buffer{}
	p := scope.New(scope.WithLogger(captureLogger(buf))).Deny("/tmp/**")

	err := p.Enforce("/tmp/x", scope.Read)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scope.ErrDenied))
	assert.Empty(t, strings.TrimSpace(buf.String()),
		"strict deny path should not emit log lines")
}

func TestSnapshot_PreservesLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	orig := scope.New(scope.WithLogger(captureLogger(buf))).
		SetMode(scope.Warn).
		Deny("/tmp/**")

	cp := orig.Snapshot()
	err := cp.Enforce("/tmp/x", scope.Read)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "scope: path denied (warn mode, allowing)",
		"Snapshot must inherit the parent policy's logger")
}
