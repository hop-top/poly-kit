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

func TestConfigWatcher_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	initial := []byte("base_url: http://localhost:6060\nstrong_model: gpt-4\n")
	require.NoError(t, os.WriteFile(path, initial, 0o644))

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

	// Mutate the file — ensure mtime advances.
	time.Sleep(100 * time.Millisecond)
	updated := []byte("base_url: http://remote:8080\nstrong_model: claude-4\n")
	require.NoError(t, os.WriteFile(path, updated, 0o644))

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
