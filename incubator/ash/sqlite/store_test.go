package sqlite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"hop.top/ash"
	ashsqlite "hop.top/ash/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

func newTestStore(t *testing.T) *ashsqlite.Store {
	t.Helper()
	s, err := ashsqlite.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCRUDRoundTrip(t *testing.T) {
	store := newTestStore(t)

	meta := ash.SessionMeta{
		ID:       "s-1",
		Metadata: map[string]any{"model": "claude"},
	}
	require.NoError(t, store.Create(ctx, meta))

	// Append a turn.
	turn := ash.Turn{
		ID:        "t-1",
		Role:      ash.RoleUser,
		Content:   "hello",
		Timestamp: time.Now().UTC(),
		Metadata:  map[string]any{"source": "cli"},
	}
	require.NoError(t, store.AppendTurn(ctx, "s-1", turn))

	loaded, err := store.Load(ctx, "s-1")
	require.NoError(t, err)
	assert.Equal(t, "s-1", loaded.ID)
	assert.Equal(t, "claude", loaded.Metadata["model"])
	require.Len(t, loaded.Turns, 1)
	assert.Equal(t, ash.RoleUser, loaded.Turns[0].Role)
	assert.Equal(t, "hello", loaded.Turns[0].Content)
	assert.Equal(t, "cli", loaded.Turns[0].Metadata["source"])
	assert.Equal(t, 0, loaded.Turns[0].Seq)
}

func TestLoadNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Load(ctx, "nonexistent")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestAppendTurnAutoSeq(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Create(ctx, ash.SessionMeta{ID: "s-1"}))

	for i := range 5 {
		turn := ash.Turn{
			ID:        fmt.Sprintf("t-%d", i),
			Role:      ash.RoleUser,
			Content:   fmt.Sprintf("msg %d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, store.AppendTurn(ctx, "s-1", turn))
	}

	loaded, err := store.Load(ctx, "s-1")
	require.NoError(t, err)
	require.Len(t, loaded.Turns, 5)

	for i, turn := range loaded.Turns {
		assert.Equal(t, i, turn.Seq, "turn %d should have seq %d", i, i)
	}
}

func TestAppendTurnToNonexistentSession(t *testing.T) {
	store := newTestStore(t)
	turn := ash.Turn{
		ID:        "t-1",
		Role:      ash.RoleUser,
		Content:   "hello",
		Timestamp: time.Now().UTC(),
	}
	err := store.AppendTurn(ctx, "no-such-session", turn)
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestTurnOrderingBySeq(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Create(ctx, ash.SessionMeta{ID: "s-1"}))

	// Insert turns with decreasing timestamps but ascending seq.
	for i := range 3 {
		turn := ash.Turn{
			ID:        fmt.Sprintf("t-%d", i),
			Role:      ash.RoleUser,
			Content:   fmt.Sprintf("msg %d", i),
			Timestamp: now.Add(-time.Duration(i) * time.Second),
		}
		require.NoError(t, store.AppendTurn(ctx, "s-1", turn))
	}

	loaded, err := store.Load(ctx, "s-1")
	require.NoError(t, err)
	require.Len(t, loaded.Turns, 3)

	// Verify ordered by seq, not timestamp.
	assert.Equal(t, "t-0", loaded.Turns[0].ID)
	assert.Equal(t, "t-1", loaded.Turns[1].ID)
	assert.Equal(t, "t-2", loaded.Turns[2].ID)
}

func TestListFilterByParentID(t *testing.T) {
	store := newTestStore(t)

	require.NoError(t, store.Create(ctx, ash.SessionMeta{ID: "s-1"}))
	require.NoError(t, store.Create(ctx, ash.SessionMeta{
		ID: "s-2", ParentID: "s-1",
	}))
	require.NoError(t, store.Create(ctx, ash.SessionMeta{
		ID: "s-3", ParentID: "s-1",
	}))

	children, err := store.List(ctx, ash.Filter{ParentID: "s-1"})
	require.NoError(t, err)
	assert.Len(t, children, 2)
}

func TestListLimitOffset(t *testing.T) {
	store := newTestStore(t)

	for i := range 5 {
		require.NoError(t, store.Create(ctx, ash.SessionMeta{
			ID: fmt.Sprintf("s-%d", i),
		}))
		// Small sleep to ensure distinct created_at timestamps.
		time.Sleep(time.Millisecond)
	}

	page, err := store.List(ctx, ash.Filter{Limit: 2, Offset: 1})
	require.NoError(t, err)
	assert.Len(t, page, 2)
}

func TestListTurnCount(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, ash.SessionMeta{ID: "s-1"}))

	for i := range 3 {
		require.NoError(t, store.AppendTurn(ctx, "s-1", ash.Turn{
			ID:        fmt.Sprintf("t-%d", i),
			Role:      ash.RoleUser,
			Content:   "msg",
			Timestamp: time.Now().UTC(),
		}))
	}

	metas, err := store.List(ctx, ash.Filter{})
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, 3, metas[0].TurnCount)
}

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, ash.SessionMeta{ID: "s-del"}))
	require.NoError(t, store.AppendTurn(ctx, "s-del", ash.Turn{
		ID: "t-1", Role: ash.RoleUser, Content: "hi",
		Timestamp: time.Now().UTC(),
	}))

	require.NoError(t, store.Delete(ctx, "s-del"))
	_, err := store.Load(ctx, "s-del")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestDeleteNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.Delete(ctx, "no-such")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestMigrationIdempotency(t *testing.T) {
	store1, err := ashsqlite.New(":memory:")
	require.NoError(t, err)
	store1.Close()

	store2, err := ashsqlite.New(":memory:")
	require.NoError(t, err)
	defer store2.Close()

	require.NoError(t, store2.Create(ctx, ash.SessionMeta{ID: "s-idem"}))
}
