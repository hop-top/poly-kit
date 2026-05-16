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

// newVersionedStore preserves the historic in-memory constructor used
// by the original tests. Kept so the in-memory variant of every
// scenario runs byte-identically to the pre-promotion version.
func newVersionedStore(t *testing.T) *VersionedDocumentStore {
	t.Helper()
	return NewInMemoryVersionedDocumentStore(newTestStore(t))
}

// versionedFactory is the seam each scenario uses to obtain its
// VersionedDocumentStore. The seed phase mutates the "first" store;
// reopen returns a fresh store backed by the same on-disk DB (or the
// same in-memory store when reopen is a no-op for that backend).
//
// Round-tripping the SQLite backend through Close/reopen between
// seed and assert proves history survives a process restart — the
// actual durability boundary kit serve cares about. The in-memory
// backend has no on-disk state, so its reopen is the identity (it
// just returns the same store): the in-memory variant exists to
// keep the original ephemeral assertions running unchanged.
type versionedFactory struct {
	name   string
	open   func(t *testing.T) *VersionedDocumentStore
	reopen func(t *testing.T, prev *VersionedDocumentStore) *VersionedDocumentStore
}

func inMemoryFactory() versionedFactory {
	return versionedFactory{
		name: "in-memory",
		open: func(t *testing.T) *VersionedDocumentStore {
			return newVersionedStore(t)
		},
		reopen: func(t *testing.T, prev *VersionedDocumentStore) *VersionedDocumentStore {
			// In-memory has no durable boundary to cross. Return the
			// same store so the assert phase sees the seed phase's
			// state — the goal of the in-memory variant is to keep
			// the original assertions byte-identical.
			return prev
		},
	}
}

// sqliteReopenFactory wires a VersionedDocumentStore over a real
// on-disk SQLite path so reopen can Close the connection and open a
// fresh DocumentStore + sqliteVersionStore against the same file —
// exactly what kit serve does on restart.
func sqliteReopenFactory() versionedFactory {
	type pathHolder struct{ path string }
	holders := make(map[*VersionedDocumentStore]*pathHolder)

	openAt := func(t *testing.T, path string) *VersionedDocumentStore {
		t.Helper()
		ds, err := NewDocumentStore(path)
		require.NoError(t, err)
		vs, err := NewSQLiteVersionStore(ds.DB())
		require.NoError(t, err)
		t.Cleanup(func() { _ = ds.Close() })
		store := NewVersionedDocumentStore(ds, vs)
		holders[store] = &pathHolder{path: path}
		return store
	}

	return versionedFactory{
		name: "sqlite-reopen",
		open: func(t *testing.T) *VersionedDocumentStore {
			return openAt(t, filepath.Join(t.TempDir(), "test.db"))
		},
		reopen: func(t *testing.T, prev *VersionedDocumentStore) *VersionedDocumentStore {
			t.Helper()
			h, ok := holders[prev]
			require.True(t, ok, "sqlite-reopen: missing path for prev store")
			// Close the existing DocumentStore (which owns the *sql.DB
			// shared with the version store). The on-disk file
			// persists; in-memory caches in sqliteVersionStore are
			// gone with the dropped reference.
			require.NoError(t, prev.store.Close())
			delete(holders, prev)

			// Reopen against the same on-disk path. sqldb.Open
			// configures the same pragmas that NewDocumentStore would,
			// but we go through NewDocumentStore so the additive
			// version-table migration runs again (idempotent).
			ds, err := NewDocumentStore(h.path)
			require.NoError(t, err)
			require.NotNil(t, ds.DB())

			// Sanity: the file is reachable through sqldb directly too.
			db, err := sqldb.Open(sqldb.Options{Path: h.path})
			require.NoError(t, err)
			db.Close()

			versions, err := NewSQLiteVersionStore(ds.DB())
			require.NoError(t, err)
			t.Cleanup(func() { _ = ds.Close() })
			fresh := NewVersionedDocumentStore(ds, versions)
			holders[fresh] = h
			return fresh
		},
	}
}

func versionedFactories() []versionedFactory {
	return []versionedFactory{inMemoryFactory(), sqliteReopenFactory()}
}

// runRoundTrip drives the seed-then-assert split across both
// backends. Existing assertions stay byte-identical: in-memory
// reopen returns the same store, so the assert closure sees the
// same world the seed closure built.
func runRoundTrip(t *testing.T, seed func(t *testing.T, vs *VersionedDocumentStore), assertPhase func(t *testing.T, vs *VersionedDocumentStore)) {
	t.Helper()
	for _, f := range versionedFactories() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			vs := f.open(t)
			seed(t, vs)
			vs = f.reopen(t, vs)
			assertPhase(t, vs)
		})
	}
}

func TestVersioned_CreateAddsVersion(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			history, err := vs.History(ctx, "note", "n1")
			require.NoError(t, err)
			assert.Len(t, history, 1)
			assert.Equal(t, 1, history[0].Seq)
			assert.JSONEq(t, `{"id":"n1","v":1}`, string(history[0].Data))
		},
	)
}

func TestVersioned_UpdateAddsVersion(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			history, err := vs.History(ctx, "note", "n1")
			require.NoError(t, err)
			assert.Len(t, history, 2)
			assert.Equal(t, 2, history[1].Seq)
			assert.JSONEq(t, `{"id":"n1","v":2}`, string(history[1].Data))
		},
	)
}

func TestVersioned_HistoryOrdered(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "doc", json.RawMessage(`{"id":"d1","step":1}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "doc", "d1", json.RawMessage(`{"id":"d1","step":2}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "doc", "d1", json.RawMessage(`{"id":"d1","step":3}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			history, err := vs.History(ctx, "doc", "d1")
			require.NoError(t, err)
			require.Len(t, history, 3)
			for i, v := range history {
				assert.Equal(t, i+1, v.Seq)
			}
		},
	)
}

func TestVersioned_RevertRestoresOldData(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"r1","title":"original"}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "note", "r1", json.RawMessage(`{"id":"r1","title":"changed"}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			doc, err := vs.Revert(ctx, "note", "r1", 1)
			require.NoError(t, err)
			assert.JSONEq(t, `{"id":"r1","title":"original"}`, string(doc.Data))

			got, _ := vs.Get(ctx, "note", "r1")
			assert.JSONEq(t, `{"id":"r1","title":"original"}`, string(got.Data))
		},
	)
}

func TestVersioned_RevertCreatesNewVersion(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"r2","v":1}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "note", "r2", json.RawMessage(`{"id":"r2","v":2}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Revert(ctx, "note", "r2", 1)
			require.NoError(t, err)

			history, err := vs.History(ctx, "note", "r2")
			require.NoError(t, err)
			assert.Len(t, history, 3)
			assert.Equal(t, 3, history[2].Seq)
		},
	)
}

func TestVersioned_RevertNotFound(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"r3","v":1}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Revert(ctx, "note", "r3", 99)
			assert.ErrorContains(t, err, "not found")
		},
	)
}

func TestVersioned_DeleteClearsHistory(t *testing.T) {
	runRoundTrip(t,
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"del1","v":1}`))
			require.NoError(t, err)
			_, err = vs.Update(ctx, "note", "del1", json.RawMessage(`{"id":"del1","v":2}`))
			require.NoError(t, err)
		},
		func(t *testing.T, vs *VersionedDocumentStore) {
			ctx := context.Background()
			err := vs.Delete(ctx, "note", "del1")
			require.NoError(t, err)

			_, err = vs.History(ctx, "note", "del1")
			assert.ErrorContains(t, err, "no history")
		},
	)
}
