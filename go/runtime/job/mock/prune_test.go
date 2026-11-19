package mock_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/mock"
)

func TestPrune(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	e := mock.New(job.WithNowFunc(func() time.Time { return now }))
	var _ job.Pruner = e

	done, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "d", Type: "t"})
	_, err := e.Claim(ctx, "d", "w")
	require.NoError(t, err)
	require.NoError(t, e.Complete(ctx, done, nil))
	_, _ = e.Enqueue(ctx, job.EnqueueOpts{Queue: "p", Type: "t"})
	run, _ := e.Enqueue(ctx, job.EnqueueOpts{Queue: "a", Type: "t"})
	_, err = e.Claim(ctx, "a", "w")
	require.NoError(t, err)

	cutoff := now.Add(time.Hour)
	_, err = e.Prune(ctx, cutoff, job.StatusActive)
	require.ErrorIs(t, err, domain.ErrValidation)

	n, err := e.Prune(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = e.Get(ctx, done)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = e.Get(ctx, run)
	assert.NoError(t, err)

	n, err = e.Prune(ctx, cutoff)
	require.NoError(t, err)
	assert.Zero(t, n)
}
