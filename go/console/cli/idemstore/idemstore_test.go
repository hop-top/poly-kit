package idemstore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli/idemstore"
)

func TestIdemstore_Sqlite_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	s, err := idemstore.OpenSQLite(dbPath, idemstore.DefaultTTL)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()

	// Miss before record.
	_, hit, err := s.Lookup(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, hit, "missing key returns hit=false")

	// Record then lookup.
	want := idemstore.Result{
		Key:      "k1",
		ExitCode: 0,
		Output:   []byte(`{"ok":true}`),
	}
	require.NoError(t, s.Record(ctx, "k1", want))

	got, hit, err := s.Lookup(ctx, "k1")
	require.NoError(t, err)
	require.True(t, hit, "recorded key must be a hit")
	assert.Equal(t, want.Key, got.Key)
	assert.Equal(t, want.ExitCode, got.ExitCode)
	assert.Equal(t, want.Output, got.Output)
	assert.False(t, got.Recorded.IsZero(),
		"Record must stamp Recorded when zero")
}

func TestIdemstore_Sqlite_OverwriteOnRecord(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	s, err := idemstore.OpenSQLite(dbPath, idemstore.DefaultTTL)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()

	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("v1"), ExitCode: 0,
	}))
	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("v2"), ExitCode: 7,
	}))

	got, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, []byte("v2"), got.Output, "last-write-wins")
	assert.Equal(t, 7, got.ExitCode)
}

func TestIdemstore_TTL_Expiry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	s, err := idemstore.OpenSQLite(dbPath, 10*time.Millisecond)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()
	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("v"),
	}))

	// Within TTL: hit.
	_, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	require.True(t, hit, "fresh entry must be a hit")

	// Past TTL: miss.
	time.Sleep(20 * time.Millisecond)
	_, hit, err = s.Lookup(ctx, "k")
	require.NoError(t, err)
	assert.False(t, hit, "expired entry must be a miss")
}

func TestIdemstore_TTL_DefaultsTo24h(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	// ttl=0 must promote to DefaultTTL (24h).
	s, err := idemstore.OpenSQLite(dbPath, 0)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()
	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("v"),
	}))
	_, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	assert.True(t, hit,
		"with TTL=0 promoted to default 24h, fresh entry must be a hit")
}

func TestIdemstore_Sqlite_PathRequired(t *testing.T) {
	_, err := idemstore.OpenSQLite("", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path required")
}

func TestIdemstore_Sqlite_PersistsAcrossOpens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")

	s1, err := idemstore.OpenSQLite(dbPath, idemstore.DefaultTTL)
	require.NoError(t, err)
	require.NoError(t, s1.Record(context.Background(), "persist", idemstore.Result{
		Output: []byte("survives"), ExitCode: 0,
	}))
	require.NoError(t, s1.Close())

	s2, err := idemstore.OpenSQLite(dbPath, idemstore.DefaultTTL)
	require.NoError(t, err)
	defer s2.Close()

	got, hit, err := s2.Lookup(context.Background(), "persist")
	require.NoError(t, err)
	require.True(t, hit, "data must survive Close+Open")
	assert.Equal(t, []byte("survives"), got.Output)
}

func TestIdemstore_Memory_RoundTrip(t *testing.T) {
	s := idemstore.Memory()
	defer s.Close()

	ctx := context.Background()

	_, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	assert.False(t, hit)

	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("mem"), ExitCode: 0,
	}))

	got, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, []byte("mem"), got.Output)
	assert.Equal(t, "k", got.Key)
}
