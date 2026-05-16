package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"hop.top/kit/go/console/log"
	"hop.top/kit/go/runtime/bus"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := `interval: 10s
targets:
  - name: test
    url: http://localhost
    method: GET
    timeout: 1s
    expect:
      status: 200
`
	err := os.WriteFile(filepath.Join(dir, "probe.yaml"), []byte(yaml), 0o644)
	require.NoError(t, err)

	// Change to temp dir so loadConfig finds ../probe.yaml
	sub := filepath.Join(dir, "go")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(sub))
	defer os.Chdir(orig)

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "10s", cfg.Interval)
	assert.Len(t, cfg.Targets, 1)
	assert.Equal(t, "test", cfg.Targets[0].Name)
}

func TestRunProbe_EventsEmitted(t *testing.T) {
	// Mock HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &probeConfig{
		Targets: []targetEntry{
			{
				Name:    "mock",
				URL:     srv.URL,
				Method:  "GET",
				Timeout: "2s",
				Expect:  expectConfig{Status: 200},
			},
		},
	}

	b := bus.New()
	var mu sync.Mutex
	var events []bus.Event
	b.SubscribeAsync("kit.probe.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	v := viper.New()
	logger := log.New(v)

	results := runProbe(cfg, b, logger)
	_ = b.Close(context.Background())

	require.Len(t, results, 1)
	assert.True(t, results[0].OK)
	assert.Equal(t, 200, results[0].Status)

	mu.Lock()
	defer mu.Unlock()
	// Should have at least a kit.probe.check.executed event
	require.GreaterOrEqual(t, len(events), 1)
	assert.Equal(t, bus.Topic("kit.probe.check.executed"), events[0].Topic)
}

func TestRunProbe_FailedTarget(t *testing.T) {
	// Mock server returning 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &probeConfig{
		Targets: []targetEntry{
			{
				Name:    "bad",
				URL:     srv.URL,
				Method:  "GET",
				Timeout: "2s",
				Expect:  expectConfig{Status: 200},
			},
		},
	}

	b := bus.New()
	var mu sync.Mutex
	var topics []string
	b.SubscribeAsync("kit.probe.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
	})

	v := viper.New()
	logger := log.New(v)

	results := runProbe(cfg, b, logger)
	_ = b.Close(context.Background())

	require.Len(t, results, 1)
	assert.False(t, results[0].OK)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, topics, "kit.probe.check.executed")
	assert.Contains(t, topics, "kit.probe.check.alerted")
}
