package durabletask_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/durabletask"
)

func fixedNow() time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
}

func newEngine(t *testing.T) *durabletask.Engine {
	t.Helper()
	e, err := durabletask.New("", job.WithNowFunc(fixedNow))
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })
	return e
}

func newEngineWithClock(t *testing.T, now *time.Time) *durabletask.Engine {
	t.Helper()
	e, err := durabletask.New("", job.WithNowFunc(func() time.Time {
		return *now
	}))
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })
	return e
}

func TestHappyPath(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	id, err := e.Enqueue(ctx, job.EnqueueOpts{
		Queue:   "q",
		Type:    "test",
		Payload: map[string]string{"key": "val"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	j, err := e.Claim(ctx, "q", "w1")
	require.NoError(t, err)
	require.NotNil(t, j)
	assert.Equal(t, id, j.ID)
	assert.Equal(t, job.StatusActive, j.Status)
	assert.Equal(t, "w1", j.ClaimedBy)
	assert.Equal(t, 1, j.Attempts)

	err = e.Complete(ctx, id, map[string]string{"ok": "true"})
	require.NoError(t, err)

	got, err := e.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusSucceeded, got.Status)
	assert.NotNil(t, got.CompletedAt)
}

func TestFailRetryThenComplete(t *testing.T) {
	now := fixedNow()
	e := newEngineWithClock(t, &now)
	ctx := context.Background()

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{
		Queue:       "q",
		Type:        "test",
		MaxAttempts: 3,
	})

	// First attempt: claim + fail with retry.
	j, err := e.Claim(ctx, "q", "w1")
	require.NoError(t, err)
	require.NotNil(t, j)

	err = e.Fail(ctx, id, job.FailOpts{Error: "boom", Retry: true})
	require.NoError(t, err)

	got, _ := e.Get(ctx, id)
	assert.Equal(t, job.StatusPending, got.Status)
	assert.NotNil(t, got.NextRunAt)

	// Advance time past backoff so claim succeeds.
	now = now.Add(1 * time.Hour)

	// Second attempt: claim + complete.
	j2, err := e.Claim(ctx, "q", "w2")
	require.NoError(t, err)
	require.NotNil(t, j2)
	assert.Equal(t, 2, j2.Attempts)

	require.NoError(t, e.Complete(ctx, id, nil))
	got, _ = e.Get(ctx, id)
	assert.Equal(t, job.StatusSucceeded, got.Status)
}

func TestStaleClaim(t *testing.T) {
	now := fixedNow()
	e := newEngineWithClock(t, &now)
	ctx := context.Background()

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "test"})

	_, err := e.Claim(ctx, "q", "w1")
	require.NoError(t, err)

	// Advance past expiry (default 5m claim TTL).
	now = now.Add(10 * time.Minute)

	released, err := e.ReleaseStaleClaims(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, released)

	got, _ := e.Get(ctx, id)
	assert.Equal(t, job.StatusPending, got.Status)
	assert.Empty(t, got.ClaimedBy)
}

func TestPriorityOrdering(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	// Enqueue: prio 2, prio 0, prio 1.
	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", Priority: 2})
	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", Priority: 0})
	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", Priority: 1})

	// Claim order should be 0, 1, 2.
	j1, _ := e.Claim(ctx, "q", "w")
	j2, _ := e.Claim(ctx, "q", "w")
	j3, _ := e.Claim(ctx, "q", "w")

	require.NotNil(t, j1)
	require.NotNil(t, j2)
	require.NotNil(t, j3)
	assert.Equal(t, 0, j1.Priority)
	assert.Equal(t, 1, j2.Priority)
	assert.Equal(t, 2, j3.Priority)
}

func TestScheduledJobDelay(t *testing.T) {
	now := fixedNow()
	e := newEngineWithClock(t, &now)
	ctx := context.Background()

	future := now.Add(1 * time.Hour)
	e.Enqueue(ctx, job.EnqueueOpts{
		Queue:       "q",
		Type:        "test",
		ScheduledAt: &future,
	})

	// Should not be claimable yet.
	j, err := e.Claim(ctx, "q", "w")
	require.NoError(t, err)
	assert.Nil(t, j)

	// Advance past scheduled time.
	now = now.Add(2 * time.Hour)
	j, err = e.Claim(ctx, "q", "w")
	require.NoError(t, err)
	require.NotNil(t, j)
}

func TestConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})

	var claimed atomic.Int32
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			j, err := e.Claim(ctx, "q", "w"+string(rune('0'+id)))
			if err == nil && j != nil {
				claimed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(1), claimed.Load(),
		"exactly one goroutine should win the claim")
}

func TestDeadLetter(t *testing.T) {
	now := fixedNow()
	e := newEngineWithClock(t, &now)
	ctx := context.Background()

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{
		Queue:       "q",
		Type:        "test",
		MaxAttempts: 1,
	})

	_, _ = e.Claim(ctx, "q", "w")

	// Fail with retry=true but attempts=max → terminal failure.
	err := e.Fail(ctx, id, job.FailOpts{Error: "fatal", Retry: true})
	require.NoError(t, err)

	got, _ := e.Get(ctx, id)
	assert.Equal(t, job.StatusFailed, got.Status,
		"should be terminal when attempts >= max_attempts")
}

func TestCancel(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	require.NoError(t, e.Cancel(ctx, id))

	got, _ := e.Get(ctx, id)
	assert.Equal(t, job.StatusCancelled, got.Status)
}

func TestTimeout(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	_, _ = e.Claim(ctx, "q", "w")

	require.NoError(t, e.Timeout(ctx, id))

	got, _ := e.Get(ctx, id)
	assert.Equal(t, job.StatusTimeout, got.Status)
}

func TestHeartbeat(t *testing.T) {
	now := fixedNow()
	e := newEngineWithClock(t, &now)
	ctx := context.Background()

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	_, _ = e.Claim(ctx, "q", "w")

	// Advance 3 minutes.
	now = now.Add(3 * time.Minute)

	require.NoError(t, e.Heartbeat(ctx, id))

	got, _ := e.Get(ctx, id)
	require.NotNil(t, got.ExpiresAt)
	// ExpiresAt should be now + 5m (claimTTL).
	assert.Equal(t, now.Add(5*time.Minute), *got.ExpiresAt)
}

func TestHeartbeatRejectsNonActive(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	err := e.Heartbeat(ctx, id)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestList(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q1", Type: "a"})
	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q2", Type: "b"})
	e.Enqueue(ctx, job.EnqueueOpts{Queue: "q1", Type: "c"})

	all, err := e.List(ctx, job.JobQuery{})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	q1, err := e.List(ctx, job.JobQuery{Queue: "q1"})
	require.NoError(t, err)
	assert.Len(t, q1, 2)

	limited, err := e.List(ctx, job.JobQuery{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	_, err := e.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestEnqueueValidation(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	_, err := e.Enqueue(ctx, job.EnqueueOpts{Type: "t"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	_, err = e.Enqueue(ctx, job.EnqueueOpts{Queue: "q"})
	assert.ErrorIs(t, err, domain.ErrValidation)
}
