package routellm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer guarded by a mutex. The watcher emits
// its warnings from the poll goroutine while the assertions read the
// capture from the test goroutine, so an unguarded buffer is a data
// race on the buffer's own fields — reported by -race regardless of
// whether the bytes happen to arrive intact.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Contains(sub string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.buf.Bytes(), []byte(sub))
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newCaptureLogger returns a kit/log-compatible logger that writes to
// the given buffer at WarnLevel (matches the watcher's emit level).
func newCaptureLogger(buf *syncBuffer) *charmlog.Logger {
	return charmlog.NewWithOptions(buf, charmlog.Options{
		Level: charmlog.WarnLevel,
	})
}

func TestConfigWatcher_StatFailed_LogsWarning(t *testing.T) {
	var buf syncBuffer
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
		return buf.Contains("stat failed")
	}, 500*time.Millisecond, 10*time.Millisecond,
		"watcher should emit stat-failed warning")

	w.Stop()

	out := buf.String()
	assert.Contains(t, out, "stat failed")
	assert.Contains(t, out, "path=")
	assert.Contains(t, out, "err=")
}

func TestConfigWatcher_ParseFailed_LogsWarning(t *testing.T) {
	var buf syncBuffer
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
		return buf.Contains("parse failed")
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
