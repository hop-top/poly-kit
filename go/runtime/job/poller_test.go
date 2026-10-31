package job_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
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
