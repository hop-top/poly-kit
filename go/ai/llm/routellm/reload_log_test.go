package routellm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCaptureLogger returns a kit/log-compatible logger that writes to
// the given buffer at WarnLevel (matches the watcher's emit level).
func newCaptureLogger(buf *bytes.Buffer) *charmlog.Logger {
	return charmlog.NewWithOptions(buf, charmlog.Options{
		Level: charmlog.WarnLevel,
	})
}

func TestConfigWatcher_StatFailed_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)

	// Path that does not exist forces os.Stat to fail.
	path := filepath.Join(t.TempDir(), "missing.yaml")

	w := NewConfigWatcher(path, func(RouterConfig) {}, WithLogger(logger))
	w.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Start(ctx)

	// Wait for poll loop to fire at least once.
	require.Eventually(t, func() bool {
		return bytes.Contains(buf.Bytes(), []byte("stat failed"))
	}, 500*time.Millisecond, 10*time.Millisecond,
		"watcher should emit stat-failed warning")

	w.Stop()

	out := buf.String()
	assert.Contains(t, out, "stat failed")
	assert.Contains(t, out, "path=")
	assert.Contains(t, out, "err=")
}

func TestConfigWatcher_ParseFailed_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)

	// Write a malformed YAML file so loadConfigFile errors after stat
	// succeeds.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: : valid: yaml:::"), 0o644))

	w := NewConfigWatcher(path, func(RouterConfig) {
		t.Fatal("onChange must not fire for unparseable config")
	}, WithLogger(logger))
	w.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Start(ctx)

	require.Eventually(t, func() bool {
		return bytes.Contains(buf.Bytes(), []byte("parse failed"))
	}, 500*time.Millisecond, 10*time.Millisecond,
		"watcher should emit parse-failed warning")

	w.Stop()

	out := buf.String()
	assert.Contains(t, out, "parse failed")
	assert.Contains(t, out, "path=")
	assert.Contains(t, out, "err=")
}

func TestConfigWatcher_DefaultLogger_NoPanic(t *testing.T) {
	// Without WithLogger the constructor must build a viper-aware
	// default. Driving the watcher to a stat-failed path exercises
	// the default logger end-to-end.
	path := filepath.Join(t.TempDir(), "missing.yaml")

	w := NewConfigWatcher(path, func(RouterConfig) {})
	w.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NotPanics(t, func() {
		w.Start(ctx)
		<-ctx.Done()
		w.Stop()
	})
}
