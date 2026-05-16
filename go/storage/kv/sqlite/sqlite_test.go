package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/sqlite"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(path)
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

func TestTTLExpiration(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutWithTTL(ctx, "ephemeral", []byte("x"), 50*time.Millisecond))

	val, ok, err := s.Get(ctx, "ephemeral")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("x"), val)

	time.Sleep(60 * time.Millisecond)

	_, ok, err = s.Get(ctx, "ephemeral")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestListPrefix_0xFFBytes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Keys with trailing 0xFF byte: prefixEnd increments prior byte.
	prefix := "data\xff"
	key1 := prefix + "a"
	key2 := prefix + "b"
	other := "dataz"

	require.NoError(t, s.Put(ctx, key1, []byte("1")))
	require.NoError(t, s.Put(ctx, key2, []byte("2")))
	require.NoError(t, s.Put(ctx, other, []byte("3")))

	keys, err := s.List(ctx, prefix)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{key1, key2}, keys)
	assert.NotContains(t, keys, other)

	// Pure 0xFF prefix: unbounded upper bound returns all keys >= prefix.
	allFF := "\xff\xff"
	ffKey := allFF + "x"
	require.NoError(t, s.Put(ctx, ffKey, []byte("ff")))

	keys, err = s.List(ctx, allFF)
	require.NoError(t, err)
	assert.Contains(t, keys, ffKey, "all-0xff prefix must not silently drop keys")
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
