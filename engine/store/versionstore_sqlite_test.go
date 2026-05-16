package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/sqldb"
)

// newSQLiteVersionStore opens an on-disk DocumentStore (so the
// version tables migration runs) and a SQLiteVersionStore over the
// shared *sql.DB, mirroring how kit serve will wire them.
func newSQLiteVersionStore(t *testing.T) (VersionStore, *DocumentStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })

	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	return vs, ds
}

func TestSQLiteVersionStore_AppendAndList(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Seq)
	assert.NotEmpty(t, v1.VersionID)

	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Seq)

	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Seq)
	assert.Equal(t, 2, got[1].Seq)
	assert.JSONEq(t, `{"v":1}`, string(got[0].Data))
	assert.JSONEq(t, `{"v":2}`, string(got[1].Data))
}

func TestSQLiteVersionStore_GetSnapshot(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"hello":"world"}`), nil)
	require.NoError(t, err)

	snap, err := vs.GetSnapshot(ctx, v.VersionID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"hello":"world"}`, string(snap))
}

func TestSQLiteVersionStore_GetSnapshotMissing(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	_, err := vs.GetSnapshot(ctx, "ghost")
	assert.ErrorContains(t, err, "not found")
}

func TestSQLiteVersionStore_AppendUnknownParent(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	_, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":1}`), []string{"does-not-exist"})
	assert.ErrorContains(t, err, "unknown parent")
}

func TestSQLiteVersionStore_DeleteHistoryCascades(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	_, err = vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)

	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Sanity: cascade evicted version_snapshots / parent edges and
	// the dedup path dropped the now-orphaned snapshot_blobs (both
	// payloads were unique to this doc, refcount=1 → 0 → deleted).
	var n int
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM version_parents`).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestSQLiteVersionStore_DeleteHistoryNoOp(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	// Per VersionStore contract, DeleteHistory on an unknown
	// (type, id) is a no-op and must not error.
	require.NoError(t, vs.DeleteHistory(ctx, "ghost", "x"))
}

func TestSQLiteVersionStore_LoadDAGReconstructsParents(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	v3, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":3}`), []string{v2.VersionID})
	require.NoError(t, err)

	dag, err := vs.LoadDAG(ctx, "doc", "d1")
	require.NoError(t, err)

	got, ok := dag.Get(v3.VersionID)
	require.True(t, ok)
	assert.Equal(t, []string{v2.VersionID}, got.ParentIDs)

	heads := dag.Heads()
	require.Len(t, heads, 1)
	assert.Equal(t, v3.VersionID, heads[0])

	ancestors := dag.Ancestors(v3.VersionID)
	assert.Contains(t, ancestors, v1.VersionID)
	assert.Contains(t, ancestors, v2.VersionID)
}

func TestSQLiteVersionStore_LoadDAGCacheInvalidated(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)

	dag, err := vs.LoadDAG(ctx, "doc", "d1")
	require.NoError(t, err)
	require.Len(t, dag.Heads(), 1)

	// Append after a load should cause the next LoadDAG to see the
	// new version (cache invalidated by AppendVersion).
	v2, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)

	dag2, err := vs.LoadDAG(ctx, "doc", "d1")
	require.NoError(t, err)
	heads := dag2.Heads()
	require.Len(t, heads, 1)
	assert.Equal(t, v2.VersionID, heads[0])
}

func TestSQLiteVersionStore_PersistsAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	{
		ds, err := NewDocumentStore(dbPath)
		require.NoError(t, err)
		vs, err := NewSQLiteVersionStore(ds.DB())
		require.NoError(t, err)
		v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
		require.NoError(t, err)
		_, err = vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
		require.NoError(t, err)
		require.NoError(t, ds.Close())
	}

	// Reopen — same on-disk file.
	db, err := sqldb.Open(sqldb.Options{Path: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	vs, err := NewSQLiteVersionStore(db)
	require.NoError(t, err)

	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Seq)
	assert.Equal(t, 2, got[1].Seq)
}

func TestSQLiteVersionStore_NilDB(t *testing.T) {
	_, err := NewSQLiteVersionStore(nil)
	assert.ErrorContains(t, err, "nil db")
}

// TestVersionedDocumentStore_SQLiteBackend is a basic end-to-end
// smoke test that VersionedDocumentStore's shared-tx path works
// with the SQLite VersionStore. Full conformance is P3.1's job.
func TestVersionedDocumentStore_SQLiteBackend(t *testing.T) {
	ds := newTestStore(t)
	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)

	vd := NewVersionedDocumentStore(ds, vs)
	ctx := context.Background()

	_, err = vd.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vd.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)

	hist, err := vd.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 2)
	assert.Equal(t, 1, hist[0].Seq)
	assert.Equal(t, 2, hist[1].Seq)

	doc, err := vd.Revert(ctx, "note", "n1", 1)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"n1","v":1}`, string(doc.Data))

	hist, err = vd.History(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Len(t, hist, 3)

	require.NoError(t, vd.Delete(ctx, "note", "n1"))
	_, err = vd.History(ctx, "note", "n1")
	assert.ErrorContains(t, err, "no history")
}
