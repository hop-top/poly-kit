// Package telemetry — batched HTTPS sink with on-disk spool fallback.
//
// HTTPSSink implements bus.Sink. It buffers events in memory, ships
// them as NDJSON batches via HTTP POST, and falls back to a dated
// spool file when the upstream is unreachable. A background goroutine
// owns the flush timer; Drain enqueues and signals — it never blocks
// on network I/O. When the in-memory ring is full, the oldest event
// is dropped (rather than blocking the caller) and DroppedOverflow
// is incremented.
//
// Pipeline order: ring buffer → batch render (NDJSON) → POST with
// exponential-backoff retry → on terminal failure, spool. ReplaySpool
// is opt-in and drains the spool dir, deleting files on 2xx.
//
// Spec: ADR-0035 decision #8 (spool location). Reuses the retry
// posture established by `go/runtime/notify/retry.go` and the auth
// env discovery idiom from `go/runtime/bus/network_auth_env.go`.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/bus"
)

// Defaults applied by NewHTTPSSink when no matching Option is supplied.
const (
	defaultBatchSize      = 100
	defaultFlushInterval  = 30 * time.Second
	defaultMaxSpoolBytes  = int64(16 << 20) // 16 MiB
	defaultMaxRetries     = 5
	defaultHTTPTimeout    = 10 * time.Second
	defaultRetryBase      = 1 * time.Second
	defaultRetryFactor    = 2.0
	defaultRetryMaxSleep  = 60 * time.Second
	defaultRingBufferSize = 1024

	// DefaultTelemetryAuthEnv is the env var consulted for the Bearer
	// token unless WithTelemetryAuthEnv overrides it. Matches the
	// `KIT_*` convention used by sibling telemetry env knobs.
	DefaultTelemetryAuthEnv = "KIT_TELEMETRY_AUTH_TOKEN"

	// spoolSubPath is the relative path under <XDG_STATE_HOME>/kit
	// where the dated spool files live. Per ADR-0035 decision #8.
	spoolSubPath = "telemetry/spool"

	// spoolFilePerm / spoolDirPerm match the installation_id perms —
	// a single 0600/0700 posture across the telemetry tree.
	spoolFilePerm fs.FileMode = 0o600
	spoolDirPerm  fs.FileMode = 0o700
)

// SpoolStats is the diagnostics snapshot read by `kit telemetry inspect`
// (kit-consent track) and assertable from tests.
type SpoolStats struct {
	PendingInMemory int
	SpoolFiles      int
	SpoolBytes      int64
	DroppedOverflow int64
}

// httpsConfig holds the resolved option set used to build an HTTPSSink.
// Kept unexported so only HTTPSOption closures in this package mutate it.
type httpsConfig struct {
	batchSize     int
	flushInterval time.Duration
	spoolDir      string
	maxSpoolBytes int64
	httpClient    *http.Client
	authEnv       string
	maxRetries    int
	ringCap       int
}

// HTTPSOption configures an HTTPSSink at construction time. Options
// apply in the order passed to NewHTTPSSink; later options override
// earlier ones for the same setting.
type HTTPSOption func(*httpsConfig)

// WithBatchSize sets the in-memory batch threshold. Default 100.
// Values < 1 are clamped to 1 — a 0-size batch is almost certainly a bug.
func WithBatchSize(n int) HTTPSOption {
	return func(c *httpsConfig) {
		if n < 1 {
			n = 1
		}
		c.batchSize = n
	}
}

// WithFlushInterval sets the wall-clock flush deadline. Default 30s.
// Values <= 0 disable time-based flushing (size-only).
func WithFlushInterval(d time.Duration) HTTPSOption {
	return func(c *httpsConfig) { c.flushInterval = d }
}

// WithSpoolDir overrides the default <XDG_STATE_HOME>/kit/telemetry/spool
// directory. Useful for tests and for adopters with their own state root.
func WithSpoolDir(path string) HTTPSOption {
	return func(c *httpsConfig) { c.spoolDir = path }
}

// WithMaxSpoolBytes caps the on-disk spool size. When exceeded, the
// oldest spool file is evicted. Default 16 MiB.
func WithMaxSpoolBytes(n int64) HTTPSOption {
	return func(c *httpsConfig) {
		if n < 0 {
			n = 0
		}
		c.maxSpoolBytes = n
	}
}

// WithHTTPClient overrides the default *http.Client. The caller is
// responsible for the client's timeout and transport configuration.
func WithHTTPClient(client *http.Client) HTTPSOption {
	return func(c *httpsConfig) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithTelemetryAuthEnv sets the environment variable name consulted
// for the Bearer token. Empty defaults to DefaultTelemetryAuthEnv.
// At Drain time the env is re-read; this matches the dynamic-config
// posture of bus.AuthFromEnv (env-driven, no boot-time snapshot).
func WithTelemetryAuthEnv(envVar string) HTTPSOption {
	return func(c *httpsConfig) {
		if envVar != "" {
			c.authEnv = envVar
		}
	}
}

// WithMaxRetries sets the total HTTP attempts per batch (initial try
// plus retries). Default 5. Values < 1 are clamped to 1.
func WithMaxRetries(n int) HTTPSOption {
	return func(c *httpsConfig) {
		if n < 1 {
			n = 1
		}
		c.maxRetries = n
	}
}

// WithRingCap sets the in-memory ring buffer capacity. When the ring
// is full, Drain drops the oldest event (incrementing DroppedOverflow)
// rather than blocking the caller. Default 1024.
func WithRingCap(n int) HTTPSOption {
	return func(c *httpsConfig) {
		if n < 1 {
			n = 1
		}
		c.ringCap = n
	}
}

// HTTPSSink batches telemetry events and ships them via HTTPS POST
// with on-disk spool fallback and exponential-backoff retry. Construct
// via NewHTTPSSink; the zero value is not usable. Implements bus.Sink.
type HTTPSSink struct {
	url string
	cfg httpsConfig

	// ring buffer (FIFO). Guarded by mu.
	mu     sync.Mutex
	ring   []bus.Event
	closed bool

	// flushCh signals the worker that batch threshold was crossed.
	// Buffered (cap 1) so Drain never blocks if the worker is busy.
	flushCh chan struct{}
	doneCh  chan struct{} // closed by Close to stop the worker
	stopped chan struct{} // closed by the worker when it has exited

	// Diagnostics.
	dropped atomic.Int64

	// Spool serialization. Spool I/O is serialized through a dedicated
	// mutex (separate from the ring) so a slow disk does not block
	// publishers.
	spoolMu sync.Mutex
}

// compile-time interface check.
var _ bus.Sink = (*HTTPSSink)(nil)

// NewHTTPSSink returns an HTTPSSink that POSTs batches of bus events to
// url. A background worker goroutine is launched immediately; call
// Close(ctx) to stop it.
func NewHTTPSSink(url string, opts ...HTTPSOption) (*HTTPSSink, error) {
	if url == "" {
		return nil, errors.New("telemetry: HTTPSSink url is required")
	}

	cfg := httpsConfig{
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		maxSpoolBytes: defaultMaxSpoolBytes,
		authEnv:       DefaultTelemetryAuthEnv,
		maxRetries:    defaultMaxRetries,
		ringCap:       defaultRingBufferSize,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.httpClient == nil {
		cfg.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	if cfg.spoolDir == "" {
		dir, err := defaultSpoolDir()
		if err != nil {
			return nil, fmt.Errorf("telemetry: resolve spool dir: %w", err)
		}
		cfg.spoolDir = dir
	}
	if err := os.MkdirAll(cfg.spoolDir, spoolDirPerm); err != nil {
		return nil, fmt.Errorf("telemetry: mkdir spool: %w", err)
	}

	s := &HTTPSSink{
		url:     url,
		cfg:     cfg,
		ring:    make([]bus.Event, 0, cfg.ringCap),
		flushCh: make(chan struct{}, 1),
		doneCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.run()
	return s, nil
}

// defaultSpoolDir resolves <XDG_STATE_HOME>/kit/telemetry/spool. Uses
// the same xdg.StateFile call as installid.go: we resolve a sentinel
// file inside the spool dir and strip the filename to get the dir.
func defaultSpoolDir() (string, error) {
	// xdg.StateFile resolves <XDG_STATE_HOME>/<tool>/<rel>. We ask for
	// the .keep sentinel; the directory is auto-created by xdg.
	p, err := xdg.StateFile(xdgTool, spoolSubPath+"/.keep")
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// Drain enqueues e into the ring buffer and signals the worker if the
// batch threshold has been crossed. Never blocks on network I/O.
// On full ring, the oldest event is evicted and DroppedOverflow is
// incremented; the new event is still admitted (newer-event-wins
// semantics keep the latest signal alive when shedding load).
//
// Drain satisfies the bus.Sink interface: errors here do not block
// publishers, so the only non-nil error returned is when the sink has
// already been Closed.
func (s *HTTPSSink) Drain(ctx context.Context, e bus.Event) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return os.ErrClosed
	}
	// Ring eviction: drop oldest if at cap.
	if len(s.ring) >= s.cfg.ringCap {
		// Slide left; drop element 0.
		copy(s.ring, s.ring[1:])
		s.ring = s.ring[:len(s.ring)-1]
		s.dropped.Add(1)
	}
	s.ring = append(s.ring, e)
	shouldFlush := len(s.ring) >= s.cfg.batchSize
	s.mu.Unlock()

	if shouldFlush {
		s.signalFlush()
	}
	return nil
}

// signalFlush nudges the worker. The channel is buffered cap 1, so a
// pending signal coalesces — the worker will see "flush" once and
// drain everything it can.
func (s *HTTPSSink) signalFlush() {
	select {
	case s.flushCh <- struct{}{}:
	default:
	}
}

// run owns the flush timer and consumes flush signals. It exits when
// doneCh is closed (by Close).
func (s *HTTPSSink) run() {
	defer close(s.stopped)

	var timerC <-chan time.Time
	var timer *time.Timer
	if s.cfg.flushInterval > 0 {
		timer = time.NewTimer(s.cfg.flushInterval)
		timerC = timer.C
		defer timer.Stop()
	}

	for {
		select {
		case <-s.doneCh:
			return
		case <-s.flushCh:
			s.flushOnce(context.Background())
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(s.cfg.flushInterval)
			}
		case <-timerC:
			s.flushOnce(context.Background())
			timer.Reset(s.cfg.flushInterval)
		}
	}
}

// flushOnce drains the ring into a single batch and ships it. Failures
// fall through to the spool.
func (s *HTTPSSink) flushOnce(ctx context.Context) {
	s.mu.Lock()
	if len(s.ring) == 0 {
		s.mu.Unlock()
		return
	}
	batch := make([]bus.Event, len(s.ring))
	copy(batch, s.ring)
	s.ring = s.ring[:0]
	s.mu.Unlock()

	if err := s.shipWithRetry(ctx, batch); err != nil {
		// Terminal failure → spool.
		if spoolErr := s.spool(batch); spoolErr != nil {
			// Last-resort: nothing we can do, count as dropped.
			s.dropped.Add(int64(len(batch)))
		}
	}
}

// shipWithRetry posts the batch with exponential-backoff retry. Returns
// nil on 2xx; returns the last error after maxRetries; bails immediately
// on breaker.ErrBrokenCircuit (open circuit is terminal — retrying
// would defeat the breaker).
func (s *HTTPSSink) shipWithRetry(ctx context.Context, batch []bus.Event) error {
	body, err := encodeNDJSON(batch)
	if err != nil {
		return fmt.Errorf("telemetry: encode batch: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < s.cfg.maxRetries; attempt++ {
		if attempt > 0 {
			d := exponentialSleep(attempt-1, defaultRetryBase, defaultRetryFactor, defaultRetryMaxSleep)
			if d > 0 {
				t := time.NewTimer(d)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-s.doneCh:
					t.Stop()
					return errors.New("telemetry: sink closing")
				}
			}
		}

		err := s.postOnce(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err

		// Open-circuit is terminal.
		if errors.Is(err, breaker.ErrBrokenCircuit) {
			return err
		}
	}
	return lastErr
}

// postOnce performs a single HTTP POST of the encoded NDJSON body.
func (s *HTTPSSink) postOnce(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if tok := os.Getenv(s.cfg.authEnv); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := s.cfg.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telemetry: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telemetry: http %d: %s", resp.StatusCode, bytes.TrimSpace(preview))
	}
	// Drain body so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// encodeNDJSON serializes a batch as one JSON object per line.
func encodeNDJSON(batch []bus.Event) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, ev := range batch {
		if err := enc.Encode(ev); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// spool appends the batch as NDJSON to today's spool file, then enforces
// the MaxSpoolBytes cap by evicting the oldest spool files.
func (s *HTTPSSink) spool(batch []bus.Event) error {
	body, err := encodeNDJSON(batch)
	if err != nil {
		return err
	}

	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()

	if err := os.MkdirAll(s.cfg.spoolDir, spoolDirPerm); err != nil {
		return err
	}
	path := filepath.Join(s.cfg.spoolDir, todayStamp()+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, spoolFilePerm)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return s.enforceSpoolCap()
}

// enforceSpoolCap deletes oldest spool files until total size ≤ cap.
// Caller must hold spoolMu.
func (s *HTTPSSink) enforceSpoolCap() error {
	if s.cfg.maxSpoolBytes <= 0 {
		return nil
	}
	files, err := listSpoolFiles(s.cfg.spoolDir)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	if total <= s.cfg.maxSpoolBytes {
		return nil
	}
	// Sort oldest-first by mtime.
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	for _, f := range files {
		if total <= s.cfg.maxSpoolBytes {
			break
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		total -= f.size
	}
	return nil
}

type spoolFileInfo struct {
	path  string
	size  int64
	mtime time.Time
}

func listSpoolFiles(dir string) ([]spoolFileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]spoolFileInfo, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		// Only .jsonl files belong to us.
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		out = append(out, spoolFileInfo{
			path:  filepath.Join(dir, name),
			size:  info.Size(),
			mtime: info.ModTime(),
		})
	}
	return out, nil
}

// ReplaySpool walks the spool dir, posts each file's content, and
// deletes the file on success. Failures leave the file in place for a
// later retry. Returns the first error encountered (other files are
// still attempted before returning).
func (s *HTTPSSink) ReplaySpool(ctx context.Context) error {
	s.spoolMu.Lock()
	files, err := listSpoolFiles(s.cfg.spoolDir)
	s.spoolMu.Unlock()
	if err != nil {
		return err
	}
	// Oldest first so replay order mirrors emission order.
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })

	var firstErr error
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := os.ReadFile(f.path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(body) == 0 {
			// Empty file — just remove it.
			_ = os.Remove(f.path)
			continue
		}
		if err := s.postOnce(ctx, body); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Stats returns a snapshot of in-memory and on-disk sink state.
func (s *HTTPSSink) Stats() SpoolStats {
	s.mu.Lock()
	pending := len(s.ring)
	s.mu.Unlock()

	s.spoolMu.Lock()
	files, _ := listSpoolFiles(s.cfg.spoolDir)
	s.spoolMu.Unlock()

	var total int64
	for _, f := range files {
		total += f.size
	}
	return SpoolStats{
		PendingInMemory: pending,
		SpoolFiles:      len(files),
		SpoolBytes:      total,
		DroppedOverflow: s.dropped.Load(),
	}
}

// Close stops the background worker and flushes any pending events
// within the supplied context's deadline. Pending events that cannot
// be shipped are spooled. Close satisfies bus.Sink — the interface
// signature is parameterless, but we expose the ctx-taking variant as
// CloseCtx for callers that want bounded shutdown semantics.
//
// Close is idempotent.
func (s *HTTPSSink) Close() error {
	return s.CloseCtx(context.Background())
}

// CloseCtx is the context-bounded variant of Close.
func (s *HTTPSSink) CloseCtx(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Stop the worker.
	close(s.doneCh)
	select {
	case <-s.stopped:
	case <-ctx.Done():
		// Worker is slow to exit; we still proceed with final flush
		// so callers can rely on best-effort drain.
	}

	// Final flush of anything left in the ring.
	s.mu.Lock()
	if len(s.ring) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := make([]bus.Event, len(s.ring))
	copy(batch, s.ring)
	s.ring = s.ring[:0]
	s.mu.Unlock()

	if err := s.shipWithRetry(ctx, batch); err != nil {
		// Best-effort spool.
		return s.spool(batch)
	}
	return nil
}

// todayStamp returns the YYYY-MM-DD spool filename stem for the local
// date. The dated layout (ADR-0035 #8) caps unbounded growth via
// external retention (`find -mtime +N`).
func todayStamp() string {
	return time.Now().UTC().Format("2006-01-02")
}

// exponentialSleep returns the duration to sleep before the given retry
// attempt (0-indexed: first retry = attempt 0). The schedule is base *
// factor^attempt with full jitter, capped at maxSleep. Mirrors
// notify.ExponentialBackoff but caps the result; we re-implement
// locally rather than depend on notify because the notify package
// pulls in breaker/redact transitively for its retry sink, which
// would be unused here.
func exponentialSleep(attempt int, base time.Duration, factor float64, maxSleep time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	d := float64(base) * math.Pow(factor, float64(attempt))
	if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
		return 0
	}
	if d >= float64(math.MaxInt64) {
		d = float64(math.MaxInt64)
	}
	// Full jitter.
	d = rand.Float64() * d
	out := time.Duration(d)
	if maxSleep > 0 && out > maxSleep {
		out = maxSleep
	}
	return out
}
