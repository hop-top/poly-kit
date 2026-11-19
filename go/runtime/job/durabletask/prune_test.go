package durabletask_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/durabletask"
)

// pruneFixture holds one job per status, all last updated at old
// (except fresh), so cutoff tests have a clear boundary.
type pruneFixture struct {
	e                  *durabletask.Engine
	ids                map[string]string
	old, cutoff, fresh time.Time
}

func newPruneFixture(t *testing.T) *pruneFixture {
	t.Helper()
	ctx := context.Background()
	f := &pruneFixture{
		ids:    map[string]string{},
		old:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		cutoff: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		fresh:  time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
	}
	now := f.old
	f.e = newEngineWithClock(t, &now)

	mk := func(name, queue string, fn func(id string)) {
		id, err := f.e.Enqueue(ctx, job.EnqueueOpts{
			Queue: queue, Type: "t", MaxAttempts: 1,
		})
		require.NoError(t, err)
		f.ids[name] = id
		if fn != nil {
			fn(id)
		}
	}
	claim := func(q string) {
		_, err := f.e.Claim(ctx, q, "w")
		require.NoError(t, err)
	}
	// Each finished job gets its own queue so Claim picks it.
	mk("succeeded", "s", func(id string) {
		claim("s")
		require.NoError(t, f.e.Complete(ctx, id, nil))
	})
	mk("failed", "f", func(id string) {
		claim("f")
		require.NoError(t, f.e.Fail(ctx, id, job.FailOpts{Error: "x"}))
	})
	mk("timeout", "to", func(id string) {
		claim("to")
		require.NoError(t, f.e.Timeout(ctx, id))
	})
	mk("canceled", "c", func(id string) {
		require.NoError(t, f.e.Cancel(ctx, id))
	})
	mk("active", "a", func(string) { claim("a") })
	mk("pending", "p", nil)
	// Finished but recent.
	now = f.fresh
	mk("fresh", "fr", func(id string) {
		claim("fr")
		require.NoError(t, f.e.Complete(ctx, id, nil))
	})
	return f
}

func (f *pruneFixture) exists(t *testing.T, name string) bool {
	t.Helper()
	_, err := f.e.Get(context.Background(), f.ids[name])
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, domain.ErrNotFound)
	return false
}

func TestPrune_DeletesOnlyOldFinished(t *testing.T) {
	f := newPruneFixture(t)
	n, err := f.e.Prune(context.Background(), f.cutoff)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	for _, name := range []string{"succeeded", "failed", "timeout", "canceled"} {
		assert.False(t, f.exists(t, name), name)
	}
	for _, name := range []string{"active", "pending", "fresh"} {
		assert.True(t, f.exists(t, name), name)
	}
}

func TestPrune_RunningAndPendingUntouchedEvenIfOld(t *testing.T) {
	f := newPruneFixture(t)
	// Cutoff after everything: only finished jobs may go.
	n, err := f.e.Prune(context.Background(), f.fresh.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.True(t, f.exists(t, "active"))
	assert.True(t, f.exists(t, "pending"))
	assert.False(t, f.exists(t, "fresh"))
}

func TestPrune_StatusFilter(t *testing.T) {
	f := newPruneFixture(t)
	n, err := f.e.Prune(context.Background(), f.cutoff,
		job.StatusFailed, job.StatusCancelled)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, f.exists(t, "failed"))
	assert.False(t, f.exists(t, "canceled"))
	assert.True(t, f.exists(t, "succeeded"))
	assert.True(t, f.exists(t, "timeout"))
}

func TestPrune_RejectsUnfinishedStatus(t *testing.T) {
	f := newPruneFixture(t)
	for _, s := range []job.Status{job.StatusActive, job.StatusPending} {
		n, err := f.e.Prune(context.Background(), f.fresh.Add(time.Hour),
			job.StatusSucceeded, s)
		require.ErrorIs(t, err, domain.ErrValidation)
		assert.Zero(t, n)
	}
	assert.True(t, f.exists(t, "succeeded"), "nothing deleted on rejection")
}

func TestPrune_Idempotent(t *testing.T) {
	f := newPruneFixture(t)
	n, err := f.e.Prune(context.Background(), f.cutoff)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	n, err = f.e.Prune(context.Background(), f.cutoff)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestPrune_BoundaryIsExclusive(t *testing.T) {
	f := newPruneFixture(t)
	// Finished jobs were updated exactly at f.old.
	n, err := f.e.Prune(context.Background(), f.old)
	require.NoError(t, err)
	assert.Zero(t, n)
}
