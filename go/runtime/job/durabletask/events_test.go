package durabletask_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/durabletask"
)

type recorded struct {
	topic, source string
	payload       any
}

type recPub struct {
	mu  sync.Mutex
	evs []recorded
}

func (r *recPub) Publish(_ context.Context, topic, source string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, recorded{topic, source, payload})
	return nil
}

// jobTopics returns only the job.* topics, in order.
func (r *recPub) jobTopics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.evs {
		if strings.HasPrefix(e.topic, "job.") {
			out = append(out, e.topic)
		}
	}
	return out
}

func (r *recPub) last(topic string) (job.JobEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.evs) - 1; i >= 0; i-- {
		if r.evs[i].topic == topic {
			ev, ok := r.evs[i].payload.(job.JobEvent)
			return ev, ok
		}
	}
	return job.JobEvent{}, false
}

func TestPublishesJobLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	pub := &recPub{}
	e := newDT(t, job.WithNowFunc(fixedNow), job.WithPublisher(pub))

	id, err := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", MaxAttempts: 2})
	require.NoError(t, err)
	ev, ok := pub.last(job.TopicCreated)
	require.True(t, ok, "created event must carry job.JobEvent")
	assert.Equal(t, job.JobEvent{JobID: id, Queue: "q", Type: "t", Status: "pending"}, ev)

	_, err = e.Claim(ctx, "q", "w1")
	require.NoError(t, err)
	ev, ok = pub.last(job.TopicClaimed)
	require.True(t, ok)
	assert.Equal(t, "w1", ev.WorkerID)
	assert.Equal(t, "active", ev.Status)

	require.NoError(t, e.Heartbeat(ctx, id))
	_, ok = pub.last(job.TopicHeartbeat)
	assert.True(t, ok)

	// Retry path: attempts remain.
	require.NoError(t, e.Fail(ctx, id, job.FailOpts{Error: "boom", Retry: true}))
	ev, ok = pub.last(job.TopicRetried)
	require.True(t, ok)
	assert.Equal(t, "boom", ev.Error)

	assert.Equal(t, []string{
		job.TopicCreated, job.TopicClaimed, job.TopicHeartbeat, job.TopicRetried,
	}, pub.jobTopics())
}

func TestEventsTerminalPaths(t *testing.T) {
	ctx := context.Background()
	pub := &recPub{}
	e := newDT(t, job.WithNowFunc(fixedNow), job.WithPublisher(pub))

	// succeeded
	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	_, _ = e.Claim(ctx, "q", "w")
	require.NoError(t, e.Complete(ctx, id, nil))
	ev, ok := pub.last(job.TopicSucceeded)
	require.True(t, ok)
	assert.Equal(t, "succeeded", ev.Status)

	// failed (terminal, Retry false)
	id, _ = e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	_, _ = e.Claim(ctx, "q", "w")
	require.NoError(t, e.Fail(ctx, id, job.FailOpts{Error: "x"}))
	ev, ok = pub.last(job.TopicFailed)
	require.True(t, ok)
	assert.Equal(t, "x", ev.Error)

	// timeout
	id, _ = e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	_, _ = e.Claim(ctx, "q", "w")
	require.NoError(t, e.Timeout(ctx, id))
	_, ok = pub.last(job.TopicTimeout)
	assert.True(t, ok)

	// canceled
	id, _ = e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	require.NoError(t, e.Cancel(ctx, id))
	_, ok = pub.last(job.TopicCancelled)
	assert.True(t, ok)

	// retries exhausted: failed + dead_letter
	id, _ = e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", MaxAttempts: 1})
	_, _ = e.Claim(ctx, "q", "w")
	require.NoError(t, e.Fail(ctx, id, job.FailOpts{Error: "last", Retry: true}))
	_, ok = pub.last(job.TopicDeadLetter)
	assert.True(t, ok)
}

func TestEventsStaleReleased(t *testing.T) {
	ctx := context.Background()
	pub := &recPub{}
	now := fixedNow()
	e := newDT(t, job.WithNowFunc(func() time.Time { return now }), job.WithPublisher(pub))

	id, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", ClaimTTL: time.Minute})
	_, _ = e.Claim(ctx, "q", "w")
	now = now.Add(2 * time.Minute)
	n, err := e.ReleaseStaleClaims(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	ev, ok := pub.last(job.TopicStaleReleased)
	require.True(t, ok)
	assert.Equal(t, id, ev.JobID)
	assert.Equal(t, "pending", ev.Status)
}

func TestClaimTTLHonored(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)

	short, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", ClaimTTL: time.Minute, Priority: 1})
	dflt, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t", Priority: 2})

	j, err := e.Claim(ctx, "q", "w")
	require.NoError(t, err)
	require.Equal(t, short, j.ID)
	require.NotNil(t, j.ExpiresAt)
	assert.Equal(t, fixedNow().Add(time.Minute), *j.ExpiresAt)

	j, err = e.Claim(ctx, "q", "w")
	require.NoError(t, err)
	require.Equal(t, dflt, j.ID)
	assert.Equal(t, fixedNow().Add(5*time.Minute), *j.ExpiresAt, "zero ClaimTTL keeps 5m default")

	require.NoError(t, e.Heartbeat(ctx, short))
	got, _ := e.Get(ctx, short)
	assert.Equal(t, fixedNow().Add(time.Minute), *got.ExpiresAt, "heartbeat reuses per-job TTL")
}

func newDT(t *testing.T, opts ...job.Option) *durabletask.Engine {
	t.Helper()
	e, err := durabletask.New("", opts...)
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })
	return e
}
