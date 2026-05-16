package ash_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"hop.top/ash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_CreateLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID:        "s-1",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]any{"model": "claude"},
	}))

	sess, err := s.Load(ctx, "s-1")
	require.NoError(t, err)
	assert.Equal(t, "s-1", sess.ID)
	assert.Empty(t, sess.Turns)
	assert.Equal(t, "claude", sess.Metadata["model"])
}

func TestMemoryStore_LoadNotFound(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	_, err := s.Load(ctx, "missing")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestMemoryStore_AppendTurnOrdering(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	for i := range 5 {
		require.NoError(t, s.AppendTurn(ctx, "s-1", ash.Turn{
			ID:        fmt.Sprintf("t-%d", i),
			Role:      ash.RoleUser,
			Content:   fmt.Sprintf("msg-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}))
	}

	sess, err := s.Load(ctx, "s-1")
	require.NoError(t, err)
	require.Len(t, sess.Turns, 5)

	for i, turn := range sess.Turns {
		assert.Equal(t, fmt.Sprintf("t-%d", i), turn.ID)
		assert.Equal(t, fmt.Sprintf("msg-%d", i), turn.Content)
	}
}

func TestMemoryStore_AppendTurnToMissing(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	err := s.AppendTurn(ctx, "missing", ash.Turn{ID: "t-0"})
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestMemoryStore_ListWithFilter(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		id := fmt.Sprintf("s-%d", i)
		require.NoError(t, s.Create(ctx, ash.SessionMeta{
			ID:        id,
			ParentID:  "root",
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			UpdatedAt: base.Add(time.Duration(i) * time.Hour),
		}))
	}

	// Filter by parent.
	metas, err := s.List(ctx, ash.Filter{ParentID: "root"})
	require.NoError(t, err)
	assert.Len(t, metas, 5)

	// Filter by time window.
	after := base.Add(1 * time.Hour)
	before := base.Add(4 * time.Hour)
	metas, err = s.List(ctx, ash.Filter{
		After:  after,
		Before: before,
	})
	require.NoError(t, err)
	assert.Len(t, metas, 2) // s-2, s-3

	// Limit + offset.
	metas, err = s.List(ctx, ash.Filter{
		ParentID: "root",
		Limit:    2,
		Offset:   1,
	})
	require.NoError(t, err)
	assert.Len(t, metas, 2)
}

func TestMemoryStore_Delete(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	require.NoError(t, s.Delete(ctx, "s-1"))

	_, err := s.Load(ctx, "s-1")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestMemoryStore_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	err := s.Delete(ctx, "missing")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestMemoryStore_ConcurrentAppendTurn(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func(idx int) {
			defer wg.Done()
			_ = s.AppendTurn(ctx, "s-1", ash.Turn{
				ID:      fmt.Sprintf("t-%d", idx),
				Role:    ash.RoleUser,
				Content: "concurrent",
			})
		}(i)
	}
	wg.Wait()

	sess, err := s.Load(ctx, "s-1")
	require.NoError(t, err)
	assert.Len(t, sess.Turns, n)
}

func TestMemoryStore_AppendTurnClosedSession(t *testing.T) {
	ctx := context.Background()
	s := ash.NewMemoryStore()

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	// Append a turn to confirm it works while open.
	require.NoError(t, s.AppendTurn(ctx, "s-1", ash.Turn{
		ID: "t-0", Role: ash.RoleUser, Content: "open",
	}))

	// Close the session via CloseSession helper.
	require.NoError(t, s.CloseSession(ctx, "s-1"))

	// Now AppendTurn should return ErrSessionClosed.
	err := s.AppendTurn(ctx, "s-1", ash.Turn{
		ID: "t-1", Role: ash.RoleUser, Content: "closed",
	})
	assert.ErrorIs(t, err, ash.ErrSessionClosed)
}
