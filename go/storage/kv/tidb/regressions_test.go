package tidb_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	"hop.top/kit/go/storage/kv/tidb"
)

// T-0732: prefixEnd overflow caused 0xff-heavy prefixes to return empty results.

func TestRegression_T0732_FFPrefixReturnsResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	store, err := tidb.New(dsn, "reg_ff_prefix")
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Use alphanumeric prefix where byte order matches MySQL utf8mb4_0900
	// collation order. Special chars (~ etc.) have UCA weights that differ
	// from byte order, breaking prefixEnd range scans. Raw byte prefix
	// tests are in unit tests and sqlite integration tests.
	prefix := "zprefix_"
	key1 := prefix + "a"
	key2 := prefix + "b"
	other := "other_key"

	require.NoError(t, store.Put(ctx, key1, []byte("1")))
	require.NoError(t, store.Put(ctx, key2, []byte("2")))
	require.NoError(t, store.Put(ctx, other, []byte("3")))

	keys, err := store.List(ctx, prefix)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{key1, key2}, keys)
	assert.NotContains(t, keys, other)
}

func TestRegression_T0732_EmptyPrefixListsAll(t *testing.T) {
	// The all-0xff unbounded prefixEnd path cannot be integration-tested
	// against MySQL (utf8mb4 rejects invalid UTF-8). It is covered by
	// unit tests (TestPrefixEnd) and sqlite integration tests. This test
	// verifies the empty-prefix unbounded path instead.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	store, err := tidb.New(dsn, "reg_allkeys")
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "alpha", []byte("1")))
	require.NoError(t, store.Put(ctx, "beta", []byte("2")))
	require.NoError(t, store.Put(ctx, "gamma", []byte("3")))

	keys, err := store.List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, keys, 3, "empty prefix must return all keys")
	assert.ElementsMatch(t, []string{"alpha", "beta", "gamma"}, keys)
}

func TestRegression_T0732_NormalPrefixBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	store, err := tidb.New(dsn, "reg_boundary")
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "abc", []byte("1")))
	require.NoError(t, store.Put(ctx, "abd", []byte("2")))

	keys, err := store.List(ctx, "ab")
	require.NoError(t, err)
	sort.Strings(keys)
	assert.Equal(t, []string{"abc", "abd"}, keys,
		`List("ab") must return both "abc" and "abd"`)

	keys, err = store.List(ctx, "abc")
	require.NoError(t, err)
	assert.Equal(t, []string{"abc"}, keys,
		`List("abc") must return only "abc"`)
}
