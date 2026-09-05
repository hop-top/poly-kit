package routellm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAtomic writes data to path via a temp file in the same directory
// followed by a rename. rename(2) is atomic within a filesystem, so a
// concurrent reader sees either the whole old file or the whole new one
// and never a zero-length window. This is the write pattern adopters
// should use, and the one the happy-path watcher tests exercise; the
// truncating-writer case the watcher must also survive has its own test.
func writeAtomic(t *testing.T, path string, data []byte) {
	t.Helper()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml.tmp")
	require.NoError(t, err)
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	_, err = tmp.Write(data)
	require.NoError(t, err)
	require.NoError(t, tmp.Chmod(0o644))
	require.NoError(t, tmp.Close())
	require.NoError(t, os.Rename(tmpName, path))
}

func TestConfigWatcher_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	initial := []byte("base_url: http://localhost:6060\nstrong_model: gpt-4\n")
	writeAtomic(t, path, initial)

	got := make(chan RouterConfig, 2)

	w := NewConfigWatcher(path, func(cfg RouterConfig) {
		got <- cfg
	})
	// Speed up polling for test.
	w.interval = 50 * time.Millisecond

	ctx := context.Background()
	w.Start(ctx)

	// First callback fires on initial detection (mtime != zero).
	select {
	case cfg := <-got:
		assert.Equal(t, "http://localhost:6060", cfg.BaseURL)
		assert.Equal(t, "gpt-4", cfg.StrongModel)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial callback")
	}

	// Mutate the file — ensure mtime advances. The write is atomic
	// (temp file plus rename), so the watcher can never observe a
	// zero-length intermediate state and this test asserts only the
	// change-detection behavior it is named for.
	time.Sleep(100 * time.Millisecond)
	updated := []byte("base_url: http://remote:8080\nstrong_model: claude-4\n")
	writeAtomic(t, path, updated)

	select {
	case cfg := <-got:
		assert.Equal(t, "http://remote:8080", cfg.BaseURL)
		assert.Equal(t, "claude-4", cfg.StrongModel)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for change callback")
	}

	w.Stop()
}

func TestConfigWatcher_StopWithoutStart(t *testing.T) {
	w := NewConfigWatcher("/nonexistent", func(RouterConfig) {})
	// Stop on an unstarted watcher must not panic.
	// Close the done channel manually so Stop doesn't block.
	close(w.done)
	w.Stop()
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	content := []byte("base_url: http://test:1234\ngrpc_port: 9999\nrouters:\n  - mf\n  - bert\n")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	cfg, err := loadConfigFile(path)
	require.NoError(t, err)
	assert.Equal(t, "http://test:1234", cfg.BaseURL)
	assert.Equal(t, 9999, cfg.GRPCPort)
	assert.Equal(t, []string{"mf", "bert"}, cfg.Routers)
}

func TestLoadConfigFile_Missing(t *testing.T) {
	_, err := loadConfigFile("/no/such/file.yaml")
	assert.Error(t, err)
}

// TestConfigWatcher_TruncatedWrite_NeverDeliversEmptyConfig pins the
// truncating-writer race deterministically: os.WriteFile truncates
// before it writes, so a poll landing in that window stats a file whose
// mtime already advanced and reads zero bytes. yaml.Unmarshal accepts
// empty input without error, so the pre-fix watcher handed a zero-value
// RouterConfig to onChange as if it were a real reload.
//
// The interleaving is forced rather than raced: the file is truncated to
// zero length, the watcher is given time for several ticks, and only
// then is the real content written.
func TestConfigWatcher_TruncatedWrite_NeverDeliversEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	initial := []byte("base_url: http://localhost:6060\nstrong_model: gpt-4\n")
	writeAtomic(t, path, initial)

	got := make(chan RouterConfig, 8)
	w := NewConfigWatcher(path, func(cfg RouterConfig) { got <- cfg })
	w.interval = 10 * time.Millisecond

	w.Start(context.Background())
	defer w.Stop()

	select {
	case cfg := <-got:
		require.Equal(t, "http://localhost:6060", cfg.BaseURL)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial callback")
	}

	// Stage 1: truncate in place — the mtime advances, the content is
	// gone. This is exactly the state os.WriteFile leaves behind between
	// its O_TRUNC open and its write.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Let several ticks observe the truncated file.
	time.Sleep(80 * time.Millisecond)

	// Nothing may have been delivered from the zero-length window.
	select {
	case cfg := <-got:
		t.Fatalf("watcher delivered a config from a zero-length read: %+v", cfg)
	default:
	}

	// Stage 2: the writer finishes. The real content must still arrive.
	updated := []byte("base_url: http://remote:8080\nstrong_model: claude-4\n")
	writeAtomic(t, path, updated)

	select {
	case cfg := <-got:
		assert.Equal(t, "http://remote:8080", cfg.BaseURL)
		assert.Equal(t, "claude-4", cfg.StrongModel)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-truncation content")
	}
}

// TestConfigWatcher_EmptyFile_IsNotDelivered covers the steady state
// rather than the transient one: a file that stays zero-length is never
// handed to onChange. A zero-length file is indistinguishable from a
// truncated one at the byte level, so the watcher fails safe and keeps
// the last good config in place.
func TestConfigWatcher_EmptyFile_IsNotDelivered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	got := make(chan RouterConfig, 4)
	w := NewConfigWatcher(path, func(cfg RouterConfig) { got <- cfg })
	w.interval = 10 * time.Millisecond

	w.Start(context.Background())
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	select {
	case cfg := <-got:
		t.Fatalf("watcher delivered a config for a zero-length file: %+v", cfg)
	default:
	}
}

// TestConfigWatcher_ExplicitlyEmptyConfig_IsDelivered is the other half
// of the empty-vs-truncated distinction: a document that is present but
// carries no keys ("{}") is a legitimate adopter state and must reach
// onChange as a zero-value RouterConfig.
func TestConfigWatcher_ExplicitlyEmptyConfig_IsDelivered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o644))

	got := make(chan RouterConfig, 4)
	w := NewConfigWatcher(path, func(cfg RouterConfig) { got <- cfg })
	w.interval = 10 * time.Millisecond

	w.Start(context.Background())
	defer w.Stop()

	select {
	case cfg := <-got:
		assert.Equal(t, RouterConfig{}, cfg)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: an explicitly empty document must be delivered")
	}
}

// TestConfigWatcher_ParseFailure_RetriesSameMtime pins that a failed
// load does not consume the mtime. Before the fix lastMod advanced
// before the load ran, so a file caught mid-write was skipped until it
// changed a second time — on a file written once and never touched
// again, the good content was never picked up at all.
//
// The file is written unparseable and then repaired with the SAME mtime
// restored, so only a watcher that retries an unconsumed mtime can ever
// deliver it.
func TestConfigWatcher_ParseFailure_RetriesSameMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(path, []byte("not: : valid: yaml:::"), 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)
	badMod := info.ModTime()

	got := make(chan RouterConfig, 4)
	w := NewConfigWatcher(path, func(cfg RouterConfig) { got <- cfg },
		WithLogger(newCaptureLogger(&syncBuffer{})))
	w.interval = 10 * time.Millisecond

	w.Start(context.Background())
	defer w.Stop()

	// Let the watcher observe and reject the unparseable content.
	time.Sleep(60 * time.Millisecond)
	select {
	case cfg := <-got:
		t.Fatalf("watcher delivered unparseable config: %+v", cfg)
	default:
	}

	// Repair the content but pin the mtime back to the rejected value.
	require.NoError(t, os.WriteFile(path,
		[]byte("base_url: http://remote:8080\n"), 0o644))
	require.NoError(t, os.Chtimes(path, badMod, badMod))

	select {
	case cfg := <-got:
		assert.Equal(t, "http://remote:8080", cfg.BaseURL)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: a rejected mtime must stay eligible for retry")
	}
}
