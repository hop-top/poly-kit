package cmdsurface

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
)

// plainStore hides a store's TTL support, the way etcd and tidb
// store.
type plainStore struct{ kv.Store }

func ledgerAt(store kv.Store, at *time.Time) *UsageLedger {
	l := NewUsageLedger(store)
	l.now = func() time.Time { return *at }
	return l
}

func TestUsageLedger_TakeCountsUpToTheLimitPerWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 10, 15, 0, 0, time.UTC)
	l := ledgerAt(memory.New(), &now)

	for i := 1; i <= 2; i++ {
		u, ok, err := l.Take(ctx, "alice", time.Hour, 2)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.EqualValues(t, i, u.Ops)
	}
	u, ok, err := l.Take(ctx, "alice", time.Hour, 2)
	require.NoError(t, err)
	assert.False(t, ok, "the third call in the window is over the limit")
	assert.EqualValues(t, 2, u.Ops, "a refused call is not counted")
	assert.Equal(t, time.Date(2026, 9, 28, 11, 0, 0, 0, time.UTC), u.Reset)

	_, ok, _ = l.Take(ctx, "bob", time.Hour, 2)
	assert.True(t, ok, "keys count separately")

	now = now.Add(time.Hour)
	u, ok, err = l.Take(ctx, "alice", time.Hour, 2)
	require.NoError(t, err)
	assert.True(t, ok, "the next window starts over")
	assert.EqualValues(t, 1, u.Ops)
}

func TestUsageLedger_SurvivesANewLedgerOnTheSameStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	store := memory.New()
	_, err := ledgerAt(store, &now).Add(ctx, "alice", time.Hour, 3, 100)
	require.NoError(t, err)

	u, err := ledgerAt(store, &now).Usage(ctx, "alice", time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 3, u.Ops)
	assert.EqualValues(t, 100, u.Bytes)
}

func TestUsageLedger_StoreWithoutTTLDropsPastWindows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	store := plainStore{memory.New()}
	l := ledgerAt(store, &now)
	_, err := l.Add(ctx, "alice", time.Hour, 1, 0)
	require.NoError(t, err)
	now = now.Add(2 * time.Hour)
	_, err = l.Add(ctx, "alice", time.Hour, 1, 0)
	require.NoError(t, err)

	stored, err := store.List(ctx, entryPrefix("alice"))
	require.NoError(t, err)
	assert.Len(t, stored, 1, "only the current window is kept")
}

func TestUsageLedger_KeysAndReset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	l := ledgerAt(memory.New(), &now)
	for _, k := range []string{"quota/api/p/alice/", "quota/api/p/a/b", "policy/x"} {
		_, err := l.Add(ctx, k, time.Hour, 1, 0)
		require.NoError(t, err)
	}
	keys, err := l.Keys(ctx, "quota/")
	require.NoError(t, err)
	assert.Equal(t, []string{"quota/api/p/a/b", "quota/api/p/alice/"}, keys)

	require.NoError(t, l.Reset(ctx, "quota/api/p/a/b"))
	keys, err = l.Keys(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"policy/x", "quota/api/p/alice/"}, keys)
}

func TestUsageLedger_RefusesASubSecondWindow(t *testing.T) {
	t.Parallel()
	l := NewUsageLedger(memory.New())
	_, _, err := l.Take(context.Background(), "k", time.Millisecond, 1)
	require.Error(t, err)
}
