package idemstore_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/idemstore"
)

func rows(t *testing.T, s idemstore.Store) int {
	t.Helper()
	n, err := idemstore.Rows(s)
	require.NoError(t, err)
	return n
}

func TestIdemstore_Sqlite_PurgesExpiredRowsOnOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := idemstore.OpenSQLite(dbPath, time.Hour)
	require.NoError(t, err)
	require.NoError(t, s.Record(ctx, "old", idemstore.Result{Output: []byte("o"), Recorded: now.Add(-2 * time.Hour)}))
	require.NoError(t, s.Record(ctx, "fresh", idemstore.Result{Output: []byte("f"), Recorded: now}))
	require.NoError(t, s.Close())

	s, err = idemstore.OpenSQLite(dbPath, time.Hour)
	require.NoError(t, err)
	defer s.Close()
	assert.Equal(t, 1, rows(t, s), "open must delete the expired rows")
	_, hit, err := s.Lookup(ctx, "fresh")
	require.NoError(t, err)
	assert.True(t, hit, "open must keep the rows within the TTL")
}

func TestIdemstore_Sqlite_PurgesExpiredRowsPeriodically(t *testing.T) {
	ctx := context.Background()
	s, err := idemstore.OpenSQLite(filepath.Join(t.TempDir(), "idem.db"), time.Hour)
	require.NoError(t, err)
	defer s.Close()
	current := time.Now().UTC()
	idemstore.SetNow(s, func() time.Time { return current })

	require.NoError(t, s.Record(ctx, "old", idemstore.Result{Output: []byte("o"), Recorded: current.Add(-2 * time.Hour)}))
	assert.Equal(t, 1, rows(t, s), "no purge runs twice within its interval")

	current = current.Add(time.Hour + time.Minute)
	require.NoError(t, s.Record(ctx, "new", idemstore.Result{Output: []byte("n")}))
	assert.Equal(t, 1, rows(t, s), "a write after the interval purges the expired rows")
	_, hit, err := s.Lookup(ctx, "new")
	require.NoError(t, err)
	assert.True(t, hit)
}

// RFC3339Nano drops a zero fraction, so a row recorded on a whole
// second sorts as text after a cutoff half a second later in the same
// second. The purge compares instants.
func TestIdemstore_Sqlite_PurgeComparesInstantsNotText(t *testing.T) {
	ctx := context.Background()
	s, err := idemstore.OpenSQLite(filepath.Join(t.TempDir(), "idem.db"), time.Hour)
	require.NoError(t, err)
	defer s.Close()
	recorded := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	current := recorded.Add(time.Hour + 500*time.Millisecond)
	idemstore.SetNow(s, func() time.Time { return current })

	require.NoError(t, s.Record(ctx, "edge", idemstore.Result{Output: []byte("e"), Recorded: recorded}))
	assert.Equal(t, 0, rows(t, s), "a row expired by half a second must be purged")
}

// A periodic purge deletes a bounded batch; a backlog larger than the
// batch drains over the following writes rather than in one.
func TestIdemstore_Sqlite_PeriodicPurgeIsBounded(t *testing.T) {
	ctx := context.Background()
	s, err := idemstore.OpenSQLite(filepath.Join(t.TempDir(), "idem.db"), time.Hour)
	require.NoError(t, err)
	defer s.Close()
	current := time.Now().UTC()
	idemstore.SetNow(s, func() time.Time { return current })
	idemstore.SetPurgeBatch(s, 2)

	for i := range 5 {
		require.NoError(t, s.Record(ctx, fmt.Sprintf("old-%d", i),
			idemstore.Result{Output: []byte("o"), Recorded: current.Add(-2 * time.Hour)}))
	}
	current = current.Add(2 * time.Hour)

	require.NoError(t, s.Record(ctx, "a", idemstore.Result{Output: []byte("a")}))
	assert.Equal(t, 3+1, rows(t, s), "one purge deletes at most one batch")
	require.NoError(t, s.Record(ctx, "b", idemstore.Result{Output: []byte("b")}))
	assert.Equal(t, 1+2, rows(t, s), "a full batch leaves the next write to continue")
	require.NoError(t, s.Record(ctx, "c", idemstore.Result{Output: []byte("c")}))
	assert.Equal(t, 0+3, rows(t, s), "the backlog drains")
}

func TestIdemstore_Sqlite_UnboundedTTLPurgesNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idem.db")
	ctx := context.Background()
	ttl := time.Duration(math.MaxInt64)

	s, err := idemstore.OpenSQLite(dbPath, ttl)
	require.NoError(t, err)
	require.NoError(t, s.Record(ctx, "ancient", idemstore.Result{
		Output: []byte("a"), Recorded: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, s.Close())

	s, err = idemstore.OpenSQLite(dbPath, ttl)
	require.NoError(t, err)
	defer s.Close()
	assert.Equal(t, 1, rows(t, s))
}
