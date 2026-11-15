package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/sqldb"
)

// TestSQLiteVersionStore_WritersNotStarved: SQLite's busy handler
// polls with backoff and has no queue, so writers contending for the
// write lock in one process can lose the race repeatedly until
// busy_timeout expires (SQLITE_BUSY). The store must queue in-process
// writers itself. A tiny busy_timeout makes the starvation window
// reachable without multi-second CI load.
func TestSQLiteVersionStore_WritersNotStarved(t *testing.T) {
	ds, err := newDocumentStore(sqldb.Options{
		Path:        filepath.Join(t.TempDir(), "busy.db"),
		Synchronous: "OFF",
		BusyTimeout: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() })
	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)

	const writers, per = 16, 25
	ctx := context.Background()
	var wg sync.WaitGroup
	errCh := make(chan error, writers*per)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_, err := vs.AppendVersion(ctx, "doc", "busy",
					json.RawMessage(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)), nil)
				if err != nil {
					errCh <- fmt.Errorf("writer %d step %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	list, err := vs.ListVersions(ctx, "doc", "busy")
	require.NoError(t, err)
	require.Len(t, list, writers*per)
}
