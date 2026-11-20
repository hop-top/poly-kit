package job_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/mock"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func testLogger(buf *syncBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// failingSvc wraps a Service and injects errors into chosen calls.
type failingSvc struct {
	job.Service
	claimErr, completeErr, failErr error
}

func (f *failingSvc) Claim(ctx context.Context, q, w string) (*job.Job, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return f.Service.Claim(ctx, q, w)
}

func (f *failingSvc) Complete(ctx context.Context, id string, r any) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	return f.Service.Complete(ctx, id, r)
}

func (f *failingSvc) Fail(ctx context.Context, id string, o job.FailOpts) error {
	if f.failErr != nil {
		return f.failErr
	}
	return f.Service.Fail(ctx, id, o)
}

// runUntil runs p until cond is true or the deadline passes.
func runUntil(t *testing.T, p *job.Poller, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "poller must keep running until ctx canceled")
	case <-time.After(3 * time.Second):
		t.Fatal("poller did not stop after cancel")
	}
	require.True(t, cond(), "condition not met before deadline")
}

func TestPollerRecoversHandlerPanic(t *testing.T) {
	ctx := context.Background()
	svc := mock.New()
	bad, _ := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "boom", MaxAttempts: 1, Priority: 1})
	good, _ := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "ok", Priority: 2})

	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Millisecond,
		Logger: testLogger(buf),
		Handlers: job.HandlerMap{
			"boom": func(context.Context, job.Job) error { panic("kaboom") },
			"ok":   func(context.Context, job.Job) error { return nil },
		},
	}
	runUntil(t, p, func() bool {
		g, _ := svc.Get(ctx, good)
		return g.Status == job.StatusSucceeded
	})

	b, _ := svc.Get(ctx, bad)
	assert.Equal(t, job.StatusFailed, b.Status)
	assert.Contains(t, b.Error, "panic")
	assert.Contains(t, b.Error, "kaboom")
	assert.Contains(t, buf.String(), "kaboom")
}

func TestPanicErrorIsTyped(t *testing.T) {
	err := error(&job.PanicError{Value: "x"})
	var pe *job.PanicError
	require.True(t, errors.As(err, &pe))
	assert.Contains(t, err.Error(), "panic: x")
}

func TestRunOneRecoversPanicWithTypedError(t *testing.T) {
	ctx := context.Background()
	svc := mock.New()
	id, _ := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "boom", MaxAttempts: 1})

	err := job.RunOne(ctx, svc, "q", "w", job.HandlerMap{
		"boom": func(context.Context, job.Job) error { panic("kaboom") },
	}, job.WithCooldownDir(t.TempDir()))
	var pe *job.PanicError
	require.ErrorAs(t, err, &pe)
	got, _ := svc.Get(ctx, id)
	assert.Equal(t, job.StatusFailed, got.Status)
}

func TestPollerSurfacesServiceErrors(t *testing.T) {
	ctx := context.Background()
	inner := mock.New()
	_, _ = inner.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "ok"})
	_, _ = inner.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "bad", Priority: 1})
	_, _ = inner.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "nohandler", Priority: 2})

	svc := &failingSvc{
		Service:     inner,
		claimErr:    nil,
		completeErr: errors.New("complete-down"),
		failErr:     errors.New("fail-down"),
	}
	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Millisecond,
		Logger: testLogger(buf),
		Handlers: job.HandlerMap{
			"ok":  func(context.Context, job.Job) error { return nil },
			"bad": func(context.Context, job.Job) error { return errors.New("handler-err") },
		},
	}
	runUntil(t, p, func() bool {
		s := buf.String()
		return strings.Contains(s, "complete-down") && strings.Contains(s, "fail-down")
	})
	assert.Contains(t, buf.String(), "handler-err")
}

func TestPollerSurfacesClaimError(t *testing.T) {
	svc := &failingSvc{Service: mock.New(), claimErr: errors.New("claim-down")}
	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Millisecond,
		Logger: testLogger(buf),
	}
	runUntil(t, p, func() bool { return strings.Contains(buf.String(), "claim-down") })
}

// ctxSvc blocks Claim and ReleaseStaleClaims until ctx ends, then
// returns an error wrapping ctx.Err(), as a database-backed Service
// does when shutdown interrupts its query. entered is closed once the
// first call is blocked.
type ctxSvc struct {
	job.Service
	entered chan struct{}
	once    sync.Once
}

func (c *ctxSvc) block(ctx context.Context, op string) error {
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	return fmt.Errorf("store: %s: %w", op, ctx.Err())
}

func (c *ctxSvc) ReleaseStaleClaims(ctx context.Context) (int, error) {
	return 0, c.block(ctx, "stale")
}

func (c *ctxSvc) Claim(ctx context.Context, _, _ string) (*job.Job, error) {
	return nil, c.block(ctx, "claim")
}

// cancelAndWait cancels ctx once entered fires, and returns Run's error.
func cancelAndWait(t *testing.T, p *job.Poller, entered <-chan struct{}) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("service never called")
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	return nil
}

func errorLevelLogger(buf *syncBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestPoller_ShutdownCancelIsNotAnError(t *testing.T) {
	svc := &ctxSvc{Service: mock.New(), entered: make(chan struct{})}
	buf := &syncBuf{}
	// Interval 1h: shutdown must return at once, not wait out a sleep.
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Hour,
		Logger: errorLevelLogger(buf),
	}
	err := cancelAndWait(t, p, svc.entered)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, buf.String(), "shutdown must not log at error level")
}

// stale release passes; Claim is the call interrupted by shutdown.
type claimOnlyCtxSvc struct{ *ctxSvc }

func (c claimOnlyCtxSvc) ReleaseStaleClaims(context.Context) (int, error) { return 0, nil }

func TestPoller_ShutdownDuringClaimIsNotAnError(t *testing.T) {
	svc := claimOnlyCtxSvc{&ctxSvc{Service: mock.New(), entered: make(chan struct{})}}
	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Hour,
		Logger: errorLevelLogger(buf),
	}
	err := cancelAndWait(t, p, svc.entered)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, buf.String())
}

func TestPoller_DeadlineIsShutdown(t *testing.T) {
	svc := claimOnlyCtxSvc{&ctxSvc{Service: mock.New(), entered: make(chan struct{})}}
	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Hour,
		Logger: errorLevelLogger(buf),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := p.Run(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, buf.String())
}

// A context error while ctx is still live is a Service fault, not
// shutdown: it must still log at ERROR.
func TestPoller_LiveCtxCancelErrorStillLogsError(t *testing.T) {
	svc := &failingSvc{Service: mock.New(), claimErr: context.Canceled}
	buf := &syncBuf{}
	p := &job.Poller{
		Service: svc, Queue: "q", WorkerID: "w", Interval: time.Millisecond,
		Logger: errorLevelLogger(buf),
	}
	runUntil(t, p, func() bool { return strings.Contains(buf.String(), "job poller: claim") })
	assert.Contains(t, buf.String(), "level=ERROR")
}

// A real error while ctx is live logs at ERROR for stale release too.
type staleErrSvc struct{ job.Service }

func (staleErrSvc) ReleaseStaleClaims(context.Context) (int, error) {
	return 0, errors.New("stale-down")
}

func TestPoller_LiveStaleErrorStillLogsError(t *testing.T) {
	buf := &syncBuf{}
	p := &job.Poller{
		Service: staleErrSvc{mock.New()}, Queue: "q", WorkerID: "w",
		Interval: time.Millisecond, Logger: errorLevelLogger(buf),
	}
	runUntil(t, p, func() bool { return strings.Contains(buf.String(), "stale-down") })
	assert.Contains(t, buf.String(), "level=ERROR")
}

// A worker canceled mid-job: Complete/Fail fail with ctx's error. That
// is shutdown, not a queue fault: no ERROR; Warn records the unsettled job.
func TestPoller_ShutdownMidJobSettleIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(ctx context.Context, j job.Job) error
	}{
		{"complete", func(ctx context.Context, _ job.Job) error { <-ctx.Done(); return nil }},
		{"fail", func(ctx context.Context, _ job.Job) error { <-ctx.Done(); return ctx.Err() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := mock.New()
			_, _ = inner.Enqueue(context.Background(), job.EnqueueOpts{Queue: "q", Type: "t"})
			svc := &settleCtxSvc{Service: inner}
			buf := &syncBuf{}
			entered := make(chan struct{})
			var once sync.Once
			p := &job.Poller{
				Service: svc, Queue: "q", WorkerID: "w", Interval: time.Hour,
				Logger: errorLevelLogger(buf),
				Handlers: job.HandlerMap{"t": func(ctx context.Context, j job.Job) error {
					once.Do(func() { close(entered) })
					return tc.handler(ctx, j)
				}},
			}
			err := cancelAndWait(t, p, entered)
			assert.ErrorIs(t, err, context.Canceled)
			assert.True(t, svc.settled.Load(), "settle call must have been attempted")
			assert.Empty(t, buf.String(), "shutdown mid-job must not log at error level")
		})
	}
}

// settleCtxSvc makes Complete and Fail return ctx's error, as a store
// does when its ctx is already done.
type settleCtxSvc struct {
	job.Service
	settled atomic.Bool
}

func (s *settleCtxSvc) Complete(ctx context.Context, _ string, _ any) error {
	s.settled.Store(true)
	return fmt.Errorf("store: complete: %w", ctx.Err())
}

func (s *settleCtxSvc) Fail(ctx context.Context, _ string, _ job.FailOpts) error {
	s.settled.Store(true)
	return fmt.Errorf("store: fail: %w", ctx.Err())
}
