package sqlstore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/storage/sqlstore"
)

func TestStore_PutGet(t *testing.T) {
	s, err := sqlstore.Open(filepath.Join(t.TempDir(), "test.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer s.Close()
	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "key1", map[string]string{"hello": "world"}))
	var out map[string]string
	found, err := s.Get(ctx, "key1", &out)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "world", out["hello"])
}

func TestStore_MissingKey(t *testing.T) {
	s, err := sqlstore.Open(filepath.Join(t.TempDir(), "test.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer s.Close()
	var out map[string]string
	found, err := s.Get(context.Background(), "missing", &out)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_TTLExpiry(t *testing.T) {
	s, err := sqlstore.Open(filepath.Join(t.TempDir(), "test.db"), sqlstore.Options{
		TTL: 10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer s.Close()
	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "ttl-key", "value"))
	time.Sleep(20 * time.Millisecond)
	var out string
	found, err := s.Get(ctx, "ttl-key", &out)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_PutOverwrites(t *testing.T) {
	s, err := sqlstore.Open(filepath.Join(t.TempDir(), "test.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer s.Close()
	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "k", "first"))
	require.NoError(t, s.Put(ctx, "k", "second"))
	var out string
	found, err := s.Get(ctx, "k", &out)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "second", out)
}
