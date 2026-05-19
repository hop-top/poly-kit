package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"
)

// newTestSink constructs an HTTPSSink pointed at srv with a per-test
// spool dir and tight timeouts suitable for unit tests.
func newTestSink(t *testing.T, srv *httptest.Server, opts ...HTTPSOption) *HTTPSSink {
	t.Helper()
	dir := t.TempDir()
	base := []HTTPSOption{
		WithSpoolDir(dir),
		WithHTTPClient(&http.Client{Timeout: 2 * time.Second}),
		WithMaxRetries(2),
		WithFlushInterval(0), // disable time-based flush by default
	}
	all := append(base, opts...)
	s, err := NewHTTPSSink(srv.URL, all...)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleEvent(i int) bus.Event {
	return bus.NewEvent("kit.telemetry.event.recorded", "test", map[string]any{"i": i})
}

// TestNewHTTPSSink_DefaultsApplied verifies the documented defaults.
func TestNewHTTPSSink_DefaultsApplied(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := NewHTTPSSink(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer s.Close()

	if s.cfg.batchSize != 100 {
		t.Errorf("default batchSize = %d, want 100", s.cfg.batchSize)
	}
	if s.cfg.flushInterval != 30*time.Second {
		t.Errorf("default flushInterval = %v, want 30s", s.cfg.flushInterval)
	}
	if s.cfg.maxSpoolBytes != int64(16<<20) {
		t.Errorf("default maxSpoolBytes = %d, want %d", s.cfg.maxSpoolBytes, 16<<20)
	}
	if s.cfg.maxRetries != 5 {
		t.Errorf("default maxRetries = %d, want 5", s.cfg.maxRetries)
	}
	if s.cfg.authEnv != DefaultTelemetryAuthEnv {
		t.Errorf("default authEnv = %q, want %q", s.cfg.authEnv, DefaultTelemetryAuthEnv)
	}
	if !strings.Contains(s.cfg.spoolDir, filepath.Join("kit", "telemetry", "spool")) {
		t.Errorf("default spoolDir = %q, want path containing kit/telemetry/spool", s.cfg.spoolDir)
	}
}

// TestNewHTTPSSink_EmptyURL ensures the constructor rejects empty URLs.
func TestNewHTTPSSink_EmptyURL(t *testing.T) {
	_, err := NewHTTPSSink("", WithSpoolDir(t.TempDir()))
	if err == nil {
		t.Fatal("NewHTTPSSink(\"\") returned nil error")
	}
}

// TestHTTPSSink_BatchesBySize verifies size-triggered flushes ship at
// least the configured batch size per POST and the total events
// shipped equals the events pushed (modulo a final remainder still
// pending in memory).
func TestHTTPSSink_BatchesBySize(t *testing.T) {
	var posts atomic.Int32
	var counts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Count newline-terminated JSON objects.
		lines := strings.Count(strings.TrimRight(string(body), "\n"), "\n") + 1
		if len(strings.TrimSpace(string(body))) == 0 {
			lines = 0
		}
		counts.Add(int32(lines))
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestSink(t, srv, WithBatchSize(3))

	// Push one batch and wait for it to ship before queuing the next.
	// This avoids racing the worker (which drains everything it sees
	// on each signal) and gives a deterministic 3+3 split.
	for i := 0; i < 3; i++ {
		if err := s.Drain(context.Background(), sampleEvent(i)); err != nil {
			t.Fatalf("Drain %d: %v", i, err)
		}
	}
	waitFor(t, 2*time.Second, func() bool { return posts.Load() >= 1 })
	for i := 3; i < 6; i++ {
		if err := s.Drain(context.Background(), sampleEvent(i)); err != nil {
			t.Fatalf("Drain %d: %v", i, err)
		}
	}
	waitFor(t, 2*time.Second, func() bool { return posts.Load() >= 2 })

	// Now push one trailing event that should remain pending.
	if err := s.Drain(context.Background(), sampleEvent(6)); err != nil {
		t.Fatalf("Drain trailing: %v", err)
	}
	// Tiny settle window so the worker is idle before we read pending.
	time.Sleep(50 * time.Millisecond)

	if got := posts.Load(); got != 2 {
		t.Errorf("posts = %d, want 2", got)
	}
	if got := counts.Load(); got != 6 {
		t.Errorf("events shipped = %d, want 6", got)
	}
	if pending := s.Stats().PendingInMemory; pending != 1 {
		t.Errorf("PendingInMemory = %d, want 1", pending)
	}
}

// waitFor polls cond until true or the deadline elapses.
func waitFor(t *testing.T, max time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHTTPSSink_FlushesByTime verifies time-triggered flushes ship a
// sub-batch event when the flush interval elapses.
func TestHTTPSSink_FlushesByTime(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestSink(t, srv, WithBatchSize(100), WithFlushInterval(50*time.Millisecond))
	if err := s.Drain(context.Background(), sampleEvent(0)); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if posts.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if posts.Load() < 1 {
		t.Errorf("posts = %d, want >= 1 after flush interval", posts.Load())
	}
}

// TestHTTPSSink_SpoolsOn500 verifies that batches landing on a 5xx
// server after retry exhaustion are spooled to disk.
func TestHTTPSSink_SpoolsOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	spoolDir := t.TempDir()
	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(spoolDir),
		WithBatchSize(5),
		WithFlushInterval(0),
		WithMaxRetries(2),
		WithHTTPClient(&http.Client{Timeout: 1 * time.Second}),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer s.Close()

	for i := 0; i < 5; i++ {
		if err := s.Drain(context.Background(), sampleEvent(i)); err != nil {
			t.Fatalf("Drain: %v", err)
		}
	}

	// Wait for retry exhaustion → spool. Backoff is 1s base with jitter;
	// 2 attempts ≤ 1s sleep, then ship to spool.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.Stats().SpoolFiles > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stats := s.Stats()
	if stats.SpoolFiles == 0 {
		t.Fatalf("expected spool file after 500s, got SpoolFiles=0")
	}

	// Validate spool content.
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var totalLines int
	for _, ent := range entries {
		if filepath.Ext(ent.Name()) != ".jsonl" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(spoolDir, ent.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		totalLines += strings.Count(string(body), "\n")
	}
	if totalLines != 5 {
		t.Errorf("spool line count = %d, want 5", totalLines)
	}
}

// TestHTTPSSink_ReplaySpoolDrains verifies ReplaySpool POSTs each spool
// file and deletes it on 2xx.
func TestHTTPSSink_ReplaySpoolDrains(t *testing.T) {
	var posted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lines := strings.Count(string(body), "\n")
		posted.Add(int32(lines))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	spoolDir := t.TempDir()
	// Pre-populate a spool file with 5 events as NDJSON.
	var ndjson strings.Builder
	for i := 0; i < 5; i++ {
		b, _ := json.Marshal(sampleEvent(i))
		ndjson.Write(b)
		ndjson.WriteByte('\n')
	}
	spoolFile := filepath.Join(spoolDir, "2026-05-19.jsonl")
	if err := os.WriteFile(spoolFile, []byte(ndjson.String()), 0o600); err != nil {
		t.Fatalf("seed spool: %v", err)
	}

	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(spoolDir),
		WithFlushInterval(0),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer s.Close()

	if err := s.ReplaySpool(context.Background()); err != nil {
		t.Fatalf("ReplaySpool: %v", err)
	}

	if got := posted.Load(); got != 5 {
		t.Errorf("posted events = %d, want 5", got)
	}
	if _, err := os.Stat(spoolFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("spool file still exists after drain: err=%v", err)
	}
}

// TestHTTPSSink_RetryBackoff verifies the retry loop makes multiple
// attempts before falling back to the spool.
func TestHTTPSSink_RetryBackoff(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(t.TempDir()),
		WithBatchSize(1),
		WithFlushInterval(0),
		WithMaxRetries(3),
		WithHTTPClient(&http.Client{Timeout: 1 * time.Second}),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer s.Close()

	if err := s.Drain(context.Background(), sampleEvent(0)); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	// Wait for retry exhaustion → spool.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.Stats().SpoolFiles > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := attempts.Load(); got < 3 {
		t.Errorf("attempts = %d, want >= 3 (maxRetries)", got)
	}
}

// TestHTTPSSink_Auth verifies the Authorization header is set from the
// configured env var.
func TestHTTPSSink_Auth(t *testing.T) {
	t.Setenv("KIT_TELEMETRY_AUTH_TOKEN", "secret-token-abc")

	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestSink(t, srv, WithBatchSize(1))
	if err := s.Drain(context.Background(), sampleEvent(0)); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return gotAuth.Load() != nil })

	got, _ := gotAuth.Load().(string)
	if want := "Bearer secret-token-abc"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// TestHTTPSSink_RingFullDropsOldest verifies the ring eviction path
// when the buffer is at capacity, and that the caller never blocks.
func TestHTTPSSink_RingFullDropsOldest(t *testing.T) {
	// Block the server so the worker can't drain.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(t.TempDir()),
		WithBatchSize(1000), // never size-triggered in this test
		WithFlushInterval(0),
		WithRingCap(5),
		WithMaxRetries(1),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer func() {
		// Release before Close so Close's final flush can complete.
		_ = s.Close()
	}()

	// Push 20 events; ring cap is 5 → 15 should be dropped.
	start := time.Now()
	for i := 0; i < 20; i++ {
		if err := s.Drain(context.Background(), sampleEvent(i)); err != nil {
			t.Fatalf("Drain %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Errorf("Drain calls took %v, expected non-blocking (< 250ms)", elapsed)
	}

	if got := s.Stats().DroppedOverflow; got < 15 {
		t.Errorf("DroppedOverflow = %d, want >= 15", got)
	}
}

// TestHTTPSSink_SpoolEvictionOnOversize verifies the oldest spool file
// is evicted when MaxSpoolBytes is exceeded.
func TestHTTPSSink_SpoolEvictionOnOversize(t *testing.T) {
	spoolDir := t.TempDir()

	// Pre-seed a few old spool files. Their cumulative size will exceed
	// the cap once a new spool batch is added.
	for i, name := range []string{"2020-01-01.jsonl", "2020-01-02.jsonl", "2020-01-03.jsonl"} {
		p := filepath.Join(spoolDir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("x", 500)), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		// Backdate so eviction order is deterministic.
		mt := time.Now().Add(time.Duration(-100+i) * time.Hour)
		_ = os.Chtimes(p, mt, mt)
	}

	// MaxSpoolBytes = 1024 → forces eviction.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(spoolDir),
		WithMaxSpoolBytes(1024),
		WithBatchSize(1),
		WithFlushInterval(0),
		WithMaxRetries(1),
		WithHTTPClient(&http.Client{Timeout: 500 * time.Millisecond}),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}
	defer s.Close()

	// Push an event that fails → spools → eviction trips.
	if err := s.Drain(context.Background(), sampleEvent(0)); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	// Wait for spool write.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := s.Stats()
		if stats.SpoolBytes > 0 && stats.SpoolBytes <= 1024 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stats := s.Stats()
	if stats.SpoolBytes > 1024 {
		t.Errorf("SpoolBytes = %d, want <= 1024 after eviction", stats.SpoolBytes)
	}
	// Oldest seed (2020-01-01) MUST be gone.
	if _, err := os.Stat(filepath.Join(spoolDir, "2020-01-01.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected oldest spool file evicted, still present: err=%v", err)
	}
}

// TestHTTPSSink_Close verifies pending events are flushed (or spooled)
// and the worker goroutine exits.
func TestHTTPSSink_Close(t *testing.T) {
	var posted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posted.Add(int32(strings.Count(string(body), "\n")))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := NewHTTPSSink(srv.URL,
		WithSpoolDir(t.TempDir()),
		WithBatchSize(100), // never size-triggered
		WithFlushInterval(0),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.Drain(context.Background(), sampleEvent(i)); err != nil {
			t.Fatalf("Drain: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = ctx

	// Worker should be stopped.
	select {
	case <-s.stopped:
	case <-time.After(2 * time.Second):
		t.Errorf("worker did not stop after Close")
	}

	if got := posted.Load(); got != 3 {
		t.Errorf("posted = %d, want 3 (Close should drain pending)", got)
	}

	// Second Close is idempotent.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	// Drain after Close returns os.ErrClosed.
	if err := s.Drain(context.Background(), sampleEvent(99)); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Drain after Close: err = %v, want os.ErrClosed", err)
	}
}
