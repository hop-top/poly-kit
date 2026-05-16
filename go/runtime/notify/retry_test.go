package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/runtime/bus"
)

// scriptedSink returns a sequence of errors on successive Drain
// calls. When the script is exhausted, it returns the last entry
// (lets a "always fail" sink share the same struct as a
// "fail-N-then-succeed" sink).
type scriptedSink struct {
	mu       sync.Mutex
	script   []error
	calls    int
	closeErr error
	closed   int
	gotEvent bus.Event
	gotCtx   context.Context
}

func (s *scriptedSink) Drain(ctx context.Context, e bus.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	s.gotEvent = e
	s.gotCtx = ctx
	if len(s.script) == 0 {
		return nil
	}
	if idx >= len(s.script) {
		return s.script[len(s.script)-1]
	}
	return s.script[idx]
}

func (s *scriptedSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return s.closeErr
}

func (s *scriptedSink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedSink) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// fastBackoff returns a deterministic, near-zero BackoffFunc
// suitable for unit tests that exercise the retry path without
// actually sleeping.
func fastBackoff() BackoffFunc {
	return func(attempt int) time.Duration {
		return 1 * time.Millisecond
	}
}

func TestNewRetrySink_Defaults(t *testing.T) {
	t.Parallel()
	r := NewRetrySink(&scriptedSink{})
	if r.opts.maxAttempts != 3 {
		t.Fatalf("default maxAttempts = %d, want 3", r.opts.maxAttempts)
	}
	if r.opts.backoff == nil {
		t.Fatal("default backoff is nil; want non-nil")
	}
	if r.opts.deadLetter != nil {
		t.Fatal("default deadLetter is non-nil; want nil")
	}
	// And the BackoffFunc must be callable for at least attempt 0.
	if d := r.opts.backoff(0); d < 0 {
		t.Fatalf("default backoff(0) = %v, want >= 0", d)
	}
}

func TestDrain_SucceedFirstTry(t *testing.T) {
	t.Parallel()
	inner := &scriptedSink{} // no script → always nil
	var backoffCalls int32
	r := NewRetrySink(inner,
		WithBackoff(func(attempt int) time.Duration {
			atomic.AddInt32(&backoffCalls, 1)
			return 0
		}),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if err != nil {
		t.Fatalf("Drain returned %v, want nil", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&backoffCalls); got != 0 {
		t.Fatalf("backoff called %d times, want 0", got)
	}
}

func TestDrain_SucceedAfterN(t *testing.T) {
	t.Parallel()
	transient := errors.New("transient")
	inner := &scriptedSink{script: []error{transient, transient, nil}}
	var backoffCalls int32
	r := NewRetrySink(inner,
		WithMaxAttempts(5),
		WithBackoff(func(attempt int) time.Duration {
			atomic.AddInt32(&backoffCalls, 1)
			return 1 * time.Millisecond
		}),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if err != nil {
		t.Fatalf("Drain returned %v, want nil", err)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("inner called %d times, want 3", got)
	}
	if got := atomic.LoadInt32(&backoffCalls); got != 2 {
		t.Fatalf("backoff called %d times, want 2", got)
	}
}

func TestDrain_ExhaustWithoutDeadLetter(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("always fails")
	inner := &scriptedSink{script: []error{wantErr}}
	r := NewRetrySink(inner,
		WithMaxAttempts(3),
		WithBackoff(fastBackoff()),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Drain returned %v, want %v", err, wantErr)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("inner called %d times, want 3", got)
	}
}

func TestDrain_ExhaustWithDeadLetter(t *testing.T) {
	t.Parallel()
	innerErr := errors.New("inner fails")
	inner := &scriptedSink{script: []error{innerErr}}
	dl := &scriptedSink{} // succeeds
	r := NewRetrySink(inner,
		WithMaxAttempts(3),
		WithBackoff(fastBackoff()),
		WithDeadLetter(dl),
	)
	e := evt("t.dl", "payload")
	err := r.Drain(context.Background(), e)
	if err != nil {
		t.Fatalf("Drain returned %v, want nil (DL succeeded)", err)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("inner called %d times, want 3", got)
	}
	if got := dl.callCount(); got != 1 {
		t.Fatalf("DL called %d times, want 1", got)
	}
	if dl.gotEvent.Topic != e.Topic {
		t.Fatalf("DL got event %q, want %q", dl.gotEvent.Topic, e.Topic)
	}
}

func TestDrain_DeadLetterError_Returned(t *testing.T) {
	t.Parallel()
	innerErr := errors.New("inner fails")
	dlErr := errors.New("DL fails too")
	inner := &scriptedSink{script: []error{innerErr}}
	dl := &scriptedSink{script: []error{dlErr}}
	r := NewRetrySink(inner,
		WithMaxAttempts(2),
		WithBackoff(fastBackoff()),
		WithDeadLetter(dl),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, dlErr) {
		t.Fatalf("Drain returned %v, want DL error %v", err, dlErr)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner called %d times, want 2", got)
	}
	if got := dl.callCount(); got != 1 {
		t.Fatalf("DL called %d times, want 1", got)
	}
}

func TestDrain_OpenCircuit_Terminal_NoRetries(t *testing.T) {
	t.Parallel()
	// wrap ErrBrokenCircuit to verify errors.Is traversal works.
	wrapped := fmt.Errorf("webhook: %w", breaker.ErrBrokenCircuit)
	inner := &scriptedSink{script: []error{wrapped}}
	dl := &scriptedSink{}
	var backoffCalls int32
	r := NewRetrySink(inner,
		WithMaxAttempts(5),
		WithBackoff(func(attempt int) time.Duration {
			atomic.AddInt32(&backoffCalls, 1)
			return 0
		}),
		WithDeadLetter(dl),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if err != nil {
		t.Fatalf("Drain returned %v, want nil (DL succeeded)", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (open-circuit terminal)", got)
	}
	if got := dl.callCount(); got != 1 {
		t.Fatalf("DL called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&backoffCalls); got != 0 {
		t.Fatalf("backoff called %d times, want 0", got)
	}
}

func TestDrain_OpenCircuit_Terminal_NoDeadLetter_ReturnsErr(t *testing.T) {
	t.Parallel()
	inner := &scriptedSink{script: []error{breaker.ErrBrokenCircuit}}
	r := NewRetrySink(inner,
		WithMaxAttempts(5),
		WithBackoff(fastBackoff()),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, breaker.ErrBrokenCircuit) {
		t.Fatalf("Drain returned %v, want ErrBrokenCircuit", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1", got)
	}
}

func TestDrain_OpenCircuit_AfterPartialFailures(t *testing.T) {
	t.Parallel()
	transient := errors.New("transient")
	inner := &scriptedSink{
		script: []error{transient, transient, breaker.ErrBrokenCircuit, nil},
	}
	dl := &scriptedSink{}
	var backoffCalls int32
	r := NewRetrySink(inner,
		WithMaxAttempts(10),
		WithBackoff(func(attempt int) time.Duration {
			atomic.AddInt32(&backoffCalls, 1)
			return 1 * time.Millisecond
		}),
		WithDeadLetter(dl),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if err != nil {
		t.Fatalf("Drain returned %v, want nil", err)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("inner called %d times, want 3", got)
	}
	if got := atomic.LoadInt32(&backoffCalls); got != 2 {
		t.Fatalf("backoff called %d times, want 2", got)
	}
	if got := dl.callCount(); got != 1 {
		t.Fatalf("DL called %d times, want 1", got)
	}
}

// blockingSink simulates a sink whose Drain takes a long time, but
// here we use scriptedSink that returns instantly. The cancel test
// cancels during the *backoff sleep* between attempts, not during
// Drain itself, so we use a long backoff with a sync trigger.
//
// Approach: synchronize via a channel emitted from inside the
// BackoffFunc. The test goroutine waits on that channel before
// canceling, guaranteeing the cancel arrives during the timer/
// select rather than racing it.
func TestDrain_ContextCancelDuringSleep(t *testing.T) {
	t.Parallel()
	inner := &scriptedSink{script: []error{errors.New("transient")}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// signal channel: closed exactly once when the BackoffFunc is
	// called (i.e. RetrySink is about to sleep).
	sleeping := make(chan struct{})
	var once sync.Once
	r := NewRetrySink(inner,
		WithMaxAttempts(5),
		WithBackoff(func(attempt int) time.Duration {
			once.Do(func() { close(sleeping) })
			// long enough that the test will fail loudly if the
			// timer/select doesn't honor ctx cancellation.
			return 10 * time.Second
		}),
	)

	// cancel as soon as we know the sleep started — no timing race.
	go func() {
		<-sleeping
		cancel()
	}()

	start := time.Now()
	err := r.Drain(ctx, evt("t", nil))
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain returned %v, want context.Canceled", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (canceled before retry)", got)
	}
	// 1s is generous; the actual wake should be well under 100ms,
	// but we don't want flakes on heavily loaded CI runners. The
	// 10s backoff means a non-cancellable sleep would blow past
	// this bound easily.
	if elapsed > 1*time.Second {
		t.Fatalf("Drain took %v, want <1s (timer/select did not honor cancellation)", elapsed)
	}
}

func TestDrain_ContextAlreadyCanceled(t *testing.T) {
	t.Parallel()
	// Lock down behavior (b) per spec/task: the loop body calls
	// inner first with no pre-loop ctx check. Inner sees a canceled
	// ctx; inner's behavior determines what it returns. Our test
	// inner ignores ctx (returns its scripted error), so we get
	// exactly one inner call and the scripted error.
	scriptedErr := errors.New("inner ignored canceled ctx")
	inner := &scriptedSink{script: []error{scriptedErr}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewRetrySink(inner,
		WithMaxAttempts(5),
		WithBackoff(fastBackoff()),
	)
	err := r.Drain(ctx, evt("t", nil))
	// Either inner.err propagates back (after first attempt the
	// pre-sleep ctx.Err() check on attempt=1 catches the canceled
	// ctx and returns it), so we expect either scriptedErr (if
	// maxAttempts=1) OR ctx.Err. With maxAttempts=5 the loop
	// reaches the post-sleep ctx check on attempt=1 and returns
	// ctx.Canceled.
	if !errors.Is(err, context.Canceled) && !errors.Is(err, scriptedErr) {
		t.Fatalf("Drain returned %v, want context.Canceled or scripted err", err)
	}
	// inner was called at least once.
	if got := inner.callCount(); got < 1 {
		t.Fatalf("inner called %d times, want >= 1", got)
	}
	// And critically: not all 5 attempts (loop must short-circuit
	// somewhere on the canceled ctx).
	if got := inner.callCount(); got >= 5 {
		t.Fatalf("inner called %d times; canceled ctx must short-circuit before exhausting attempts", got)
	}
}

func TestWithMaxAttempts_Zero_BehavesAsOne(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("inner fails")
	inner := &scriptedSink{script: []error{wantErr}}
	r := NewRetrySink(inner, WithMaxAttempts(0), WithBackoff(fastBackoff()))
	err := r.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Drain returned %v, want %v", err, wantErr)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (zero clamped to one)", got)
	}
}

func TestWithMaxAttempts_One_NoBackoff(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("inner fails")
	inner := &scriptedSink{script: []error{wantErr}}
	r := NewRetrySink(inner,
		WithMaxAttempts(1),
		WithBackoff(func(attempt int) time.Duration {
			t.Fatalf("backoff invoked with attempt=%d; want never invoked when maxAttempts=1", attempt)
			return 0
		}),
	)
	err := r.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Drain returned %v, want %v", err, wantErr)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1", got)
	}
}

func TestClose_ClosesInnerAndDeadLetter(t *testing.T) {
	t.Parallel()
	inner := &scriptedSink{}
	dl := &scriptedSink{}
	r := NewRetrySink(inner, WithDeadLetter(dl))
	if err := r.Close(); err != nil {
		t.Fatalf("Close returned %v, want nil", err)
	}
	if got := inner.closeCount(); got != 1 {
		t.Fatalf("inner closed %d times, want 1", got)
	}
	if got := dl.closeCount(); got != 1 {
		t.Fatalf("DL closed %d times, want 1", got)
	}
}

func TestClose_InnerError_Returned(t *testing.T) {
	t.Parallel()
	innerErr := errors.New("inner close fails")
	inner := &scriptedSink{closeErr: innerErr}
	dl := &scriptedSink{}
	r := NewRetrySink(inner, WithDeadLetter(dl))
	err := r.Close()
	if !errors.Is(err, innerErr) {
		t.Fatalf("Close returned %v, want inner err %v", err, innerErr)
	}
	// DL must still have been closed.
	if got := dl.closeCount(); got != 1 {
		t.Fatalf("DL closed %d times, want 1 (must close even on inner err)", got)
	}
}

func TestClose_BothError_FirstReturned(t *testing.T) {
	t.Parallel()
	innerErr := errors.New("inner close fails")
	dlErr := errors.New("DL close fails")
	inner := &scriptedSink{closeErr: innerErr}
	dl := &scriptedSink{closeErr: dlErr}
	r := NewRetrySink(inner, WithDeadLetter(dl))
	err := r.Close()
	if !errors.Is(err, innerErr) {
		t.Fatalf("Close returned %v, want inner err (first wins)", err)
	}
	if got := dl.closeCount(); got != 1 {
		t.Fatalf("DL closed %d times, want 1 (must attempt close even after inner err)", got)
	}
}

func TestClose_NoDeadLetter(t *testing.T) {
	t.Parallel()
	inner := &scriptedSink{}
	r := NewRetrySink(inner)
	if err := r.Close(); err != nil {
		t.Fatalf("Close returned %v, want nil", err)
	}
	if got := inner.closeCount(); got != 1 {
		t.Fatalf("inner closed %d times, want 1", got)
	}
}

func TestRetrySink_ImplementsBusSink(t *testing.T) {
	t.Parallel()
	var _ bus.Sink = (*RetrySink)(nil)
}

// TestDrain_AttemptIndexing locks down the attempt-1 vs attempt
// invariant: backoff(0) on first retry, backoff(1) on second, etc.
func TestDrain_AttemptIndexing(t *testing.T) {
	t.Parallel()
	transient := errors.New("transient")
	inner := &scriptedSink{script: []error{transient}}
	var seen []int
	var mu sync.Mutex
	r := NewRetrySink(inner,
		WithMaxAttempts(4),
		WithBackoff(func(attempt int) time.Duration {
			mu.Lock()
			seen = append(seen, attempt)
			mu.Unlock()
			return 1 * time.Millisecond
		}),
	)
	_ = r.Drain(context.Background(), evt("t", nil))
	mu.Lock()
	defer mu.Unlock()
	want := []int{0, 1, 2}
	if len(seen) != len(want) {
		t.Fatalf("backoff called with %v, want %v", seen, want)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Fatalf("backoff[%d] = %d, want %d (seen=%v)", i, seen[i], w, seen)
		}
	}
}
