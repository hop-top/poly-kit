package badger_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/badger"
)

func newStore(t *testing.T) *badger.Store {
	t.Helper()
	s, err := badger.New(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetRoundtrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, "k1", []byte("v1")))
	val, ok, err := s.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("v1"), val)
}

func TestGetMissing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	val, ok, err := s.Get(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, "k1", []byte("v1")))
	require.NoError(t, s.Delete(ctx, "k1"))

	_, ok, err := s.Get(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestListPrefix(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, "app/a", []byte("1")))
	require.NoError(t, s.Put(ctx, "app/b", []byte("2")))
	require.NoError(t, s.Put(ctx, "other/c", []byte("3")))

	keys, err := s.List(ctx, "app/")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app/a", "app/b"}, keys)
}

func TestPutWithTTL(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutWithTTL(ctx, "ttl-key", []byte("val"), 1*time.Second))

	val, ok, err := s.Get(ctx, "ttl-key")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("val"), val)

	time.Sleep(2 * time.Second)

	_, ok, err = s.Get(ctx, "ttl-key")
	require.NoError(t, err)
	assert.False(t, ok, "expected key to expire after TTL")
}

func TestConcurrentAccess(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n)
			_ = s.Put(ctx, key, []byte("val"))
			_, _, _ = s.Get(ctx, key)
		}(i)
	}
	wg.Wait()
}
