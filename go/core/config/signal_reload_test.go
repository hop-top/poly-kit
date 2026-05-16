package config_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestWatchSignal_RejectsEmptyArgs(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{ListenAddr: ":8080", Endpoint: "https://x"}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}
	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	r := config.New(&loaded, opts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := r.WatchSignal(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one signal")
}

func TestWatchSignal_RejectsNilSignal(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{ListenAddr: ":8080", Endpoint: "https://x"}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}
	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	r := config.New(&loaded, opts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := r.WatchSignal(ctx, nil)
	require.Error(t, err)
}

// TestWatchSignal_E2EReloadOnSignal sends SIGUSR1 to the running
// process; WatchSignal should observe it and trigger Reload, swapping
// the snapshot. SIGHUP is intentionally NOT used — Go's test runner
// can be sensitive to it on some platforms.
func TestWatchSignal_E2EReloadOnSignal(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://before",
		Sub:        subCfg{Threshold: 1, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	pub := &recordingPublisher{}
	r := config.New(&loaded, opts, config.WithReloadPublisher(pub))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Returns when ctx is canceled.
		_ = r.WatchSignal(ctx, syscall.SIGUSR1)
	}()

	// Give WatchSignal a moment to install the signal.Notify handler.
	time.Sleep(50 * time.Millisecond)

	// Mutate the file so the post-signal Reload sees something to swap.
	updated := initial
	updated.Endpoint = "https://after"
	writeReloadYAML(t, dir, updated)

	// Send the signal.
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

	// Wait for the snapshot to swap.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().Endpoint == "https://after" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, "https://after", r.Snapshot().Endpoint)

	// And the reloaded event should have fired.
	events := waitForEvent(t, pub, string(config.DefaultReloadTopics.Reloaded), 1)
	assert.NotEmpty(t, events)

	cancel()
	// Give the watcher a moment to exit cleanly.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchSignal did not exit after ctx cancel")
	}
}

// TestWatchSignal_VetoDoesNotKillWatcher ensures an immutable-veto
// reload error does not terminate WatchSignal — the next signal must
// still be delivered.
func TestWatchSignal_VetoDoesNotKillWatcher(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://before",
		Sub:        subCfg{Threshold: 1, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	pub := &recordingPublisher{}
	r := config.New(&loaded, opts, config.WithReloadPublisher(pub))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = r.WatchSignal(ctx, syscall.SIGUSR2)
	}()
	time.Sleep(50 * time.Millisecond)

	// First reload: change immutable → veto.
	bad := initial
	bad.ListenAddr = ":9090"
	writeReloadYAML(t, dir, bad)
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))

	failed := waitForEvent(t, pub, string(config.DefaultReloadTopics.ReloadFailed), 1)
	require.Len(t, failed, 1)

	// Restore immutable, then bump mutable; second signal must still go
	// through and produce a reloaded event.
	good := initial
	good.Endpoint = "https://after"
	writeReloadYAML(t, dir, good)
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().Endpoint == "https://after" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, "https://after", r.Snapshot().Endpoint)
	cancel()
	wg.Wait()
}

// guard against unused import lint when the build tags disable some
// platforms' test cases.
var _ = filepath.Join
var _ = os.Getpid
