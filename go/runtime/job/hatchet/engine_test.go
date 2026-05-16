//go:build hatchet

package hatchet_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/hatchet"
)

// testNow returns a fixed clock for deterministic tests.
func testNow() time.Time {
	return time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
}

// stubClient implements hatchet.HatchetClient for local testing.
type stubClient struct {
	triggered []string
	cancelled []string
}

func (s *stubClient) TriggerRun(_ context.Context, queue string, _ map[string]any) (string, error) {
	id := "run_" + queue
	s.triggered = append(s.triggered, id)
	return id, nil
}

func (s *stubClient) CancelRun(_ context.Context, runID string) error {
	s.cancelled = append(s.cancelled, runID)
	return nil
}

func (s *stubClient) GetRun(_ context.Context, _ string) (string, error) {
	return "RUNNING", nil
}

func TestEnqueue(t *testing.T) {
	sc := &stubClient{}
	eng := hatchet.New(sc, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "integration",
		Type:  "test.ping",
		Payload: map[string]string{
			"message": "hello",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "run_integration", id)
	assert.Len(t, sc.triggered, 1)
}

func TestEnqueueValidation(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	_, err := eng.Enqueue(ctx, job.EnqueueOpts{Type: "test"})
	assert.Error(t, err, "queue is required")

	_, err = eng.Enqueue(ctx, job.EnqueueOpts{Queue: "q"})
	assert.Error(t, err, "type is required")
}

func TestLifecycle_ClaimComplete(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "lifecycle",
		Type:  "test.work",
	})
	require.NoError(t, err)

	// Claim.
	j, err := eng.Claim(ctx, "lifecycle", "worker-1")
	require.NoError(t, err)
	require.NotNil(t, j)
	assert.Equal(t, id, j.ID)
	assert.Equal(t, job.StatusActive, j.Status)
	assert.Equal(t, "worker-1", j.ClaimedBy)

	// Complete.
	err = eng.Complete(ctx, id, map[string]string{"done": "yes"})
	require.NoError(t, err)

	got, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusSucceeded, got.Status)
}

func TestLifecycle_Fail_Retry(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue:       "retry",
		Type:        "test.retry",
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	_, err = eng.Claim(ctx, "retry", "worker-1")
	require.NoError(t, err)

	err = eng.Fail(ctx, id, job.FailOpts{Error: "oops", Retry: true})
	require.NoError(t, err)

	j, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusPending, j.Status)
	assert.NotNil(t, j.NextRunAt)
}

func TestLifecycle_Fail_Terminal(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "terminal",
		Type:  "test.fail",
	})
	require.NoError(t, err)

	_, err = eng.Claim(ctx, "terminal", "worker-1")
	require.NoError(t, err)

	err = eng.Fail(ctx, id, job.FailOpts{Error: "fatal", Retry: false})
	require.NoError(t, err)

	j, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusFailed, j.Status)
}

func TestTimeout(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "timeout",
		Type:  "test.timeout",
	})
	require.NoError(t, err)

	_, err = eng.Claim(ctx, "timeout", "worker-1")
	require.NoError(t, err)

	err = eng.Timeout(ctx, id)
	require.NoError(t, err)

	j, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusTimeout, j.Status)
}

func TestCancel(t *testing.T) {
	sc := &stubClient{}
	eng := hatchet.New(sc, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "cancel",
		Type:  "test.cancel",
	})
	require.NoError(t, err)

	err = eng.Cancel(ctx, id)
	require.NoError(t, err)
	assert.Contains(t, sc.cancelled, id)

	j, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCancelled, j.Status)
}

func TestHeartbeat(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "heartbeat",
		Type:  "test.hb",
	})
	require.NoError(t, err)

	_, err = eng.Claim(ctx, "heartbeat", "worker-1")
	require.NoError(t, err)

	err = eng.Heartbeat(ctx, id)
	require.NoError(t, err)
}

func TestList(t *testing.T) {
	eng := hatchet.New(nil, job.WithNowFunc(testNow))
	ctx := context.Background()

	_, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "list-q",
		Type:  "test.list",
	})
	require.NoError(t, err)

	_, err = eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "list-q",
		Type:  "test.other",
	})
	require.NoError(t, err)

	jobs, err := eng.List(ctx, job.JobQuery{Queue: "list-q"})
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	jobs, err = eng.List(ctx, job.JobQuery{
		Queue: "list-q",
		Type:  "test.list",
	})
	require.NoError(t, err)
	assert.Len(t, jobs, 1)
}

func TestReleaseStaleClaims(t *testing.T) {
	now := testNow()
	eng := hatchet.New(nil, job.WithNowFunc(func() time.Time {
		return now
	}))
	ctx := context.Background()

	id, err := eng.Enqueue(ctx, job.EnqueueOpts{
		Queue: "stale",
		Type:  "test.stale",
	})
	require.NoError(t, err)

	_, err = eng.Claim(ctx, "stale", "worker-1")
	require.NoError(t, err)

	// Advance past claim TTL.
	now = now.Add(10 * time.Minute)

	released, err := eng.ReleaseStaleClaims(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, released)

	j, err := eng.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, job.StatusPending, j.Status)
}
