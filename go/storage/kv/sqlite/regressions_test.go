package sqlite_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-0732: prefixEnd overflow caused 0xff-heavy prefixes to return empty results.

func TestRegression_T0732_FFPrefixReturnsResults(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

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
}

func TestRegression_T0732_AllFFPrefixUnbounded(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, "aaa", []byte("1")))
	require.NoError(t, s.Put(ctx, "\xff\xff", []byte("2")))
	require.NoError(t, s.Put(ctx, "\xff\xffz", []byte("3")))

	keys, err := s.List(ctx, "\xff\xff")
	require.NoError(t, err)
	assert.Contains(t, keys, "\xff\xff",
		"all-0xff prefix must match itself")
	assert.Contains(t, keys, "\xff\xffz",
		"all-0xff prefix must match keys beyond prefix")
	assert.NotContains(t, keys, "aaa",
		"all-0xff prefix must not include keys below prefix")
}

func TestRegression_T0732_NormalPrefixBoundary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, "abc", []byte("1")))
	require.NoError(t, s.Put(ctx, "abd", []byte("2")))

	keys, err := s.List(ctx, "ab")
	require.NoError(t, err)
	sort.Strings(keys)
	assert.Equal(t, []string{"abc", "abd"}, keys,
		`List("ab") must return both "abc" and "abd"`)

	keys, err = s.List(ctx, "abc")
	require.NoError(t, err)
	assert.Equal(t, []string{"abc"}, keys,
		`List("abc") must return only "abc"`)
}
