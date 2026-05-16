package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/util"
	"hop.top/kit/go/storage/sqldb"
)

// seedLegacyDB synthesizes a pre-`engine-snapshot-dedup` database
// state. It creates the document/version tables but with the old
// `snapshots(version_id, data)` shape (no snapshot_blobs /
// version_snapshots), seeds rows into versions + snapshots, and
// returns the path. The boot of NewDocumentStore against this path
// triggers migrateToDedup. Used by the migration tests below.
func seedLegacyDB(t *testing.T, payloads map[string]string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sqldb.Open(sqldb.Options{Path: dbPath})
	require.NoError(t, err)
	defer db.Close()

	// Mirror the shape from the engine-versioned-sqlite track
	// (pre-dedup). versions + version_parents + the legacy
	// snapshots table; everything else (documents, etc.) is a
	// no-op for these tests.
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS versions (
	type        TEXT    NOT NULL,
	id          TEXT    NOT NULL,
	version_id  TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	hash        TEXT    NOT NULL,
	timestamp   INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	PRIMARY KEY (type, id, seq),
	UNIQUE (version_id)
);
CREATE INDEX IF NOT EXISTS idx_versions_lookup ON versions(type, id, seq);

CREATE TABLE IF NOT EXISTS version_parents (
	version_id  TEXT NOT NULL,
	parent_id   TEXT NOT NULL,
	PRIMARY KEY (version_id, parent_id),
	FOREIGN KEY (version_id) REFERENCES versions(version_id) ON DELETE CASCADE,
	FOREIGN KEY (parent_id)  REFERENCES versions(version_id)
);

CREATE TABLE IF NOT EXISTS snapshots (
	version_id  TEXT NOT NULL PRIMARY KEY,
	data        BLOB NOT NULL,
	FOREIGN KEY (version_id) REFERENCES versions(version_id) ON DELETE CASCADE
);
`)
	require.NoError(t, err)

	// Seed a versions row + snapshot for each payload key. Use a
	// stable seq order keyed alphabetically so tests are
	// deterministic.
	keys := make([]string, 0, len(payloads))
	for k := range payloads {
		keys = append(keys, k)
	}
	// Sort keys for determinism — go map iteration is random.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	for i, vid := range keys {
		data := []byte(payloads[vid])
		hash := util.Short(data, 16)
		_, err := db.Exec(
			`INSERT INTO versions (type, id, version_id, seq, hash, timestamp, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"note", "n1", vid, i+1, hash, int64(1000+i), "2026-05-07T00:00:00Z",
		)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO snapshots (version_id, data) VALUES (?, ?)`, vid, data)
		require.NoError(t, err)
	}
	return dbPath
}

// TestMigrateToDedup_LegacyTablePresent boots NewDocumentStore over
// a legacy-shape DB and asserts the migration folds the legacy rows
// into the new tables and drops the old one.
func TestMigrateToDedup_LegacyTablePresent(t *testing.T) {
	dbPath := seedLegacyDB(t, map[string]string{
		"v-aaa": `{"v":1}`,
		"v-bbb": `{"v":2}`,
		"v-ccc": `{"v":3}`,
	})

	s, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// Legacy table is dropped.
	var legacy string
	err = s.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='snapshots'`,
	).Scan(&legacy)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	// Three distinct payloads → three blob rows, three join rows.
	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 3, n)
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&n))
	assert.Equal(t, 3, n)

	// Each blob has refcount=1 — payloads were unique.
	rows, err := s.DB().Query(`SELECT refcount FROM snapshot_blobs`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var rc int64
		require.NoError(t, rows.Scan(&rc))
		assert.Equal(t, int64(1), rc)
	}
	require.NoError(t, rows.Err())

	// Roundtrip: GetSnapshot resolves through the new tables.
	vs, err := NewSQLiteVersionStore(s.DB())
	require.NoError(t, err)
	got, err := vs.GetSnapshot(context.Background(), "v-aaa")
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1}`, string(got))
}

// TestMigrateToDedup_AggregatesIdenticalPayloads asserts that when
// every legacy snapshot has the same data, exactly one
// snapshot_blobs row remains with refcount = N.
func TestMigrateToDedup_AggregatesIdenticalPayloads(t *testing.T) {
	dbPath := seedLegacyDB(t, map[string]string{
		"v-aaa": `{"v":1}`,
		"v-bbb": `{"v":1}`,
		"v-ccc": `{"v":1}`,
		"v-ddd": `{"v":1}`,
	})

	s, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 1, n, "identical payloads must collapse to one blob")
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&n))
	assert.Equal(t, 4, n, "every version still maps through the join")

	var rc int64
	require.NoError(t, s.DB().QueryRow(`SELECT refcount FROM snapshot_blobs`).Scan(&rc))
	assert.Equal(t, int64(4), rc, "refcount must reflect every reference")
}

// TestMigrateToDedup_Idempotent re-boots a store post-migration and
// asserts no second-pass mutation occurs.
func TestMigrateToDedup_Idempotent(t *testing.T) {
	dbPath := seedLegacyDB(t, map[string]string{
		"v-aaa": `{"v":1}`,
		"v-bbb": `{"v":2}`,
	})

	// First boot — runs migration.
	s, err := NewDocumentStore(dbPath)
	require.NoError(t, err)

	var blobsBefore, joinsBefore int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&blobsBefore))
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&joinsBefore))
	require.NoError(t, s.Close())

	// Second boot — must be a no-op.
	s2, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s2.Close() })

	var blobsAfter, joinsAfter int
	require.NoError(t, s2.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&blobsAfter))
	require.NoError(t, s2.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&joinsAfter))

	assert.Equal(t, blobsBefore, blobsAfter, "second boot must not change blob count")
	assert.Equal(t, joinsBefore, joinsAfter, "second boot must not change join count")

	// Legacy table still absent after second boot.
	var legacy string
	err = s2.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='snapshots'`,
	).Scan(&legacy)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// TestMigrateToDedup_FreshBoot asserts the migration is a no-op on
// a never-populated DB (the common case for new installs). The new
// tables are created by versionTablesSQL; migrateToDedup just sees
// no legacy table and returns.
func TestMigrateToDedup_FreshBoot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	s, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&n))
	assert.Equal(t, 0, n)
}

// TestDedup_AppendVersionRefcountReuse asserts the AppendVersion
// path reuses an existing blob when payloads match (the dedup
// headline win).
func TestDedup_AppendVersionRefcountReuse(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	_, err = vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), []string{v1.VersionID})
	require.NoError(t, err)

	// Single blob, refcount=2.
	var n int
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 1, n)
	var rc int64
	require.NoError(t, ds.DB().QueryRow(`SELECT refcount FROM snapshot_blobs`).Scan(&rc))
	assert.Equal(t, int64(2), rc)
}

// TestDedup_DeleteHistoryReleasesBlobs covers the refcount-decrement
// path: deleting a doc whose payloads are unique drops every blob
// to zero, which deletes the row outright.
func TestDedup_DeleteHistoryReleasesBlobs(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	_, err = vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)

	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	var n int
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 0, n)
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM version_snapshots`).Scan(&n))
	assert.Equal(t, 0, n)
}

// TestDedup_DeleteHistoryKeepsSharedBlobs covers the refcount path
// with cross-document sharing: when two distinct (type, id)
// documents share an identical payload, deleting one leaves the
// other's blob intact (refcount drops 2 → 1).
func TestDedup_DeleteHistoryKeepsSharedBlobs(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	_, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"shared":true}`), nil)
	require.NoError(t, err)
	_, err = vs.AppendVersion(ctx, "note", "n2", json.RawMessage(`{"shared":true}`), nil)
	require.NoError(t, err)

	// Pre-delete state: one blob, refcount=2.
	var rc int64
	require.NoError(t, ds.DB().QueryRow(`SELECT refcount FROM snapshot_blobs`).Scan(&rc))
	assert.Equal(t, int64(2), rc)

	// Delete n1's history; n2's blob must survive with refcount=1.
	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	var n int
	require.NoError(t, ds.DB().QueryRow(`SELECT COUNT(*) FROM snapshot_blobs`).Scan(&n))
	assert.Equal(t, 1, n)
	require.NoError(t, ds.DB().QueryRow(`SELECT refcount FROM snapshot_blobs`).Scan(&rc))
	assert.Equal(t, int64(1), rc)

	// n2's GetSnapshot still works.
	v2, err := vs.ListVersions(ctx, "note", "n2")
	require.NoError(t, err)
	require.Len(t, v2, 1)
	got, err := vs.GetSnapshot(ctx, v2[0].VersionID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"shared":true}`, string(got))
}

// TestDedup_HashCollisionSentinel is a synthetic fault-injection
// that bypasses AppendVersion's hash and writes a row whose stored
// data does not match the recomputed hash, then asserts a follow-up
// AppendVersion with the colliding hash returns ErrHashCollision.
//
// This is the only way to cover the integrity path without a real
// hash collision (which util.Short(data, 16) wouldn't produce in
// any realistic test fixture).
func TestDedup_HashCollisionSentinel(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	// Real append for payload A. Recompute its hash so we know what
	// to attack.
	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"a":1}`), nil)
	require.NoError(t, err)
	var aHash string
	require.NoError(t, ds.DB().QueryRow(
		`SELECT hash FROM version_snapshots WHERE version_id = ?`, v1.VersionID,
	).Scan(&aHash))

	// Synthesize a collision: rewrite the blob row to have payload
	// B but keep the hash from payload A. The next AppendVersion
	// for payload B will compute hash(B) = aHash (forced), see the
	// existing row, and the bytes-equal check will reject.
	_, err = ds.DB().Exec(
		`UPDATE snapshot_blobs SET data = ? WHERE hash = ?`,
		[]byte(`{"a":2}`), aHash,
	)
	require.NoError(t, err)

	// Drive the second AppendVersion with the same hash but a
	// different payload. We can't override util.Short, so we
	// instead call the helper directly to simulate the path.
	conn, err := ds.DB().Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)
	defer func() {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
	}()

	err = upsertSnapshotBlob(ctx, conn, aHash, json.RawMessage(`{"a":3}`))
	assert.ErrorIs(t, err, ErrHashCollision)
}

// TestDedup_RefcountUnderflowSentinel covers the
// decrementSnapshotBlob underflow guard with synthetic fault
// injection: corrupt the version_snapshots join to point at a hash
// with refcount=1, then add an extra row pointing to the same hash
// (so the collected count is 2). DeleteHistory will compute
// delta=2 against current=1 and surface ErrRefcountUnderflow.
func TestDedup_RefcountUnderflowSentinel(t *testing.T) {
	vs, ds := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	var hash string
	require.NoError(t, ds.DB().QueryRow(
		`SELECT hash FROM version_snapshots WHERE version_id = ?`, v1.VersionID,
	).Scan(&hash))

	// Synthetic corruption: a phantom version_snapshots row that
	// claims another version_id references the same hash. The
	// versions row exists too (created normally) — but the hash
	// it references is a different one, so the refcount is only 1
	// while the join collects 2 occurrences.
	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	// Force v2's join row to also reference v1's hash. That row is
	// now duplicated (one for v1, one for v2 falsely pointing at
	// the same hash) — refcount in snapshot_blobs is still 1 for
	// that hash from the v1 insert; the v2 insert created a
	// different hash row whose refcount is 1 too. To simulate the
	// underflow we rewrite v2's join hash AND set the v1 blob's
	// refcount to 1 instead of the natural 2 we'd expect.
	_, err = ds.DB().Exec(
		`UPDATE version_snapshots SET hash = ? WHERE version_id = ?`, hash, v2.VersionID,
	)
	require.NoError(t, err)

	// DeleteHistory now collects {hash: 2}; the blob at `hash` only
	// has refcount=1 (from v1's append). Decrement of 2 underflows.
	err = vs.DeleteHistory(ctx, "note", "n1")
	assert.ErrorIs(t, err, ErrRefcountUnderflow)
}
