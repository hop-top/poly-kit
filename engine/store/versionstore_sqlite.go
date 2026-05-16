package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"hop.top/kit/go/core/util"
	"hop.top/kit/go/runtime/domain/version"
)

// sqliteVersionStore is the SQLite-backed [VersionStore]. It writes
// to the same *sql.DB the [DocumentStore] owns so that document
// mutations and version writes can commit in a single transaction
// (spec §6). For callers who don't need cross-write atomicity
// (e.g. tests), every public VersionStore method also works on a
// standalone basis using its own transaction.
//
// Write transactions use BEGIN IMMEDIATE on a checked-out
// *sql.Conn, not the default DEFERRED that database/sql.BeginTx
// produces. With WAL + concurrent writers, a DEFERRED tx that
// starts with SELECT before INSERT (every AppendVersion) hits
// SQLITE_BUSY_SNAPSHOT (517) at lock-upgrade time — and busy_timeout
// does not retry through that error. Acquiring the reserved lock at
// transaction start removes the upgrade race entirely. The
// conformance suite's ConcurrencySmoke scenario exercises this.
//
// LoadDAG materializes lazily: the DAG for (docType, id) is built
// from versions/version_parents rows on first access and cached for
// the process lifetime. DeleteHistory invalidates the cache entry.
type sqliteVersionStore struct {
	db *sql.DB

	mu sync.RWMutex
	// dagCache holds the lazily-materialized DAG per docKey.
	// dagGen tracks a monotonic per-key generation counter that
	// increments on every cache-invalidating write
	// (AppendVersion, DeleteHistory). LoadDAG reads the
	// generation before its (unlocked) buildDAG call and only
	// caches the result if the generation hasn't moved by the
	// time it takes the WLock — otherwise the freshly built DAG
	// would shadow a more-recent invalidation and serve stale
	// rows on subsequent reads. See LoadDAG for the protocol.
	dagCache map[string]*version.DAG // key: docKey(type, id)
	dagGen   map[string]uint64       // key: docKey(type, id) → generation at last invalidation
}

// NewSQLiteVersionStore returns a [VersionStore] backed by the
// supplied *sql.DB. The required tables (versions, version_parents,
// snapshot_blobs, version_snapshots) are assumed to already exist —
// they are created by [NewDocumentStore]'s additive migration.
// NewSQLiteVersionStore pings the DB to surface connectivity errors
// early.
func NewSQLiteVersionStore(db *sql.DB) (VersionStore, error) {
	if db == nil {
		return nil, fmt.Errorf("store: NewSQLiteVersionStore: nil db")
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("store: NewSQLiteVersionStore: ping: %w", err)
	}
	return &sqliteVersionStore{
		db:       db,
		dagCache: make(map[string]*version.DAG),
		dagGen:   make(map[string]uint64),
	}, nil
}

// AppendVersion implements VersionStore. It opens a fresh
// BEGIN IMMEDIATE transaction on a dedicated connection. For the
// cross-store atomic path used by VersionedDocumentStore, see
// appendVersionTx.
func (s *sqliteVersionStore) AppendVersion(ctx context.Context, docType, id string, data json.RawMessage, parents []string) (Version, error) {
	conn, commit, err := beginImmediate(ctx, s.db)
	if err != nil {
		return Version{}, fmt.Errorf("store: append version: begin: %w", err)
	}
	v, err := s.appendVersionTx(ctx, conn, docType, id, data, parents)
	if cerr := commit(err); cerr != nil {
		if err != nil {
			return Version{}, err
		}
		return Version{}, fmt.Errorf("store: append version: commit: %w", cerr)
	}
	return v, err
}

// appendVersionTx is the tx-aware variant. VersionedDocumentStore
// calls it when it wants to commit a document write and version
// rows in one transaction. The caller owns the tx lifecycle. The
// sqlExec interface is satisfied by both *sql.Tx and *sql.Conn so
// the shared-tx path can pass either; the IMMEDIATE-locking
// discipline is enforced by the caller (beginImmediate or its
// equivalent in versioned.go).
func (s *sqliteVersionStore) appendVersionTx(ctx context.Context, tx sqlExec, docType, id string, data json.RawMessage, parents []string) (Version, error) {
	// 1. Compute the next seq for (type, id) from the
	//    version_seq_high_water table. Reading MAX(seq) over the
	//    versions table is non-monotonic across Prune — DeleteVersions
	//    drops the very rows MAX(seq) reads, so a subsequent
	//    AppendVersion would reissue a seq already used by a
	//    since-pruned version (and so collide on version_id, which
	//    util.Short derives from "type:id-seq-data"). The high-water
	//    table is only ever written upward; DeleteHistory clears it
	//    when the entire document is removed.
	//
	//    Legacy compatibility: a DB that predates this table can
	//    have versions rows but no high-water row. The COALESCE chain
	//    seeds the very first read from MAX(seq) so an upgrade-in-
	//    place doesn't restart seq counting from 1 mid-document. The
	//    INSERT OR REPLACE below materializes the high-water row on
	//    the first AppendVersion after upgrade, so the legacy MAX
	//    branch is consulted at most once per (type, id).
	var nextSeq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(
			(SELECT next_seq FROM version_seq_high_water WHERE type = ? AND id = ?),
			(SELECT MAX(seq) FROM versions WHERE type = ? AND id = ?),
			0
		) + 1`,
		docType, id, docType, id,
	).Scan(&nextSeq); err != nil {
		return Version{}, fmt.Errorf("store: append version: next seq: %w", err)
	}

	// 2. Validate every parent exists for THIS (type, id). The
	//    in-memory backend errors on unknown-parent via its
	//    per-(type, id) DAG, so a parent that lives under a
	//    different document is rejected naturally there. We mirror
	//    that contract by scoping the lookup — a global SELECT on
	//    version_id alone would accept cross-document parents and
	//    persist an invalid edge, breaking subsequent LoadDAG for
	//    the child and blocking DeleteHistory for the parent doc
	//    via the version_parents FK.
	for _, pid := range parents {
		var seen int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM versions WHERE version_id = ? AND type = ? AND id = ?`,
			pid, docType, id,
		).Scan(&seen); err != nil {
			return Version{}, fmt.Errorf("store: append version: check parent: %w", err)
		}
		if seen == 0 {
			return Version{}, fmt.Errorf("store: append version: unknown parent %s", pid)
		}
	}

	// 3. Generate version_id and timestamps. Match the in-memory
	//    backend's recipe so version IDs are stable across backends
	//    given identical inputs.
	key := docKey(docType, id)
	now := time.Now()
	vid := util.Short([]byte(fmt.Sprintf("%s-%d-%s", key, nextSeq, data)), 16)
	hash := util.Short(data, 16)
	createdAt := now.UTC().Format(time.RFC3339Nano)

	// 4. Insert into versions, version_parents, snapshot_blobs +
	//    version_snapshots. The snapshot path is content-addressed
	//    per spec §4: try to insert a fresh blob row; on hash
	//    conflict bump the existing row's refcount, with overflow
	//    and collision guards (decisions #4 / #1). The
	//    version_snapshots join row is the single source of truth
	//    that ties this version_id to its hash.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions (type, id, version_id, seq, hash, timestamp, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		docType, id, vid, nextSeq, hash, now.UnixNano(), createdAt,
	); err != nil {
		return Version{}, fmt.Errorf("store: append version: insert version: %w", err)
	}
	for _, pid := range parents {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO version_parents (version_id, parent_id) VALUES (?, ?)`, vid, pid,
		); err != nil {
			return Version{}, fmt.Errorf("store: append version: insert parent: %w", err)
		}
	}
	if err := upsertSnapshotBlob(ctx, tx, hash, data); err != nil {
		return Version{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO version_snapshots (version_id, hash) VALUES (?, ?)`, vid, hash,
	); err != nil {
		return Version{}, fmt.Errorf("store: append version: insert version_snapshot: %w", err)
	}

	// 5. Bump the high-water counter to nextSeq. INSERT OR REPLACE
	//    materializes the row on first append (or after a legacy
	//    upgrade where the COALESCE seeded from MAX(seq)) and
	//    overwrites the prior value on subsequent appends. The write
	//    is monotonic by construction — DeleteVersions never
	//    decrements the counter, so the next AppendVersion's read
	//    above always sees the largest seq ever issued for this
	//    (type, id).
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO version_seq_high_water (type, id, next_seq) VALUES (?, ?, ?)`,
		docType, id, nextSeq,
	); err != nil {
		return Version{}, fmt.Errorf("store: append version: bump high-water: %w", err)
	}

	// 6. Invalidate the cached DAG for this key — next LoadDAG will
	//    rebuild from rows. Cheaper than incrementally mutating the
	//    cache and keeps the cache consistent with whatever's on
	//    disk. Bumping dagGen makes the invalidation visible to a
	//    concurrent LoadDAG that may have already started building
	//    a stale DAG (it'll discard the result on store).
	s.mu.Lock()
	delete(s.dagCache, key)
	s.dagGen[key]++
	s.mu.Unlock()

	return Version{
		Type:      docType,
		ID:        id,
		VersionID: vid,
		Seq:       nextSeq,
		Data:      append(json.RawMessage(nil), data...),
		CreatedAt: createdAt,
		Live:      true, // every version is born live; DEFAULT 1 in versions.live
	}, nil
}

// ListVersions implements VersionStore.
//
// Hydrates Live from the `live` column (T-0425): SQLite INTEGER 1 =
// true, 0 = false. The column is NOT NULL DEFAULT 1, so every row
// has a defined value.
func (s *sqliteVersionStore) ListVersions(ctx context.Context, docType, id string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT version_id, seq, created_at, live FROM versions WHERE type = ? AND id = ? ORDER BY seq`,
		docType, id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list versions: %w", err)
	}
	defer rows.Close()

	var out []Version
	for rows.Next() {
		var v Version
		v.Type = docType
		v.ID = id
		var liveInt int
		if err := rows.Scan(&v.VersionID, &v.Seq, &v.CreatedAt, &liveInt); err != nil {
			return nil, fmt.Errorf("store: list versions: scan: %w", err)
		}
		v.Live = liveInt != 0
		// Hydrate the snapshot so the wire shape matches the
		// in-memory backend's ListVersions output.
		data, err := s.getSnapshotTx(ctx, nil, v.VersionID)
		if err != nil {
			return nil, err
		}
		v.Data = data
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetSnapshot implements VersionStore.
func (s *sqliteVersionStore) GetSnapshot(ctx context.Context, versionID string) (json.RawMessage, error) {
	return s.getSnapshotTx(ctx, nil, versionID)
}

// getSnapshotTx looks up a snapshot using the supplied executor if
// non-nil, otherwise the underlying DB. Reads through the
// version_snapshots join into snapshot_blobs (spec §4).
func (s *sqliteVersionStore) getSnapshotTx(ctx context.Context, tx sqlExec, versionID string) (json.RawMessage, error) {
	const q = `SELECT b.data
	           FROM version_snapshots vs
	           JOIN snapshot_blobs   b  ON b.hash = vs.hash
	           WHERE vs.version_id = ?`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, q, versionID)
	} else {
		row = s.db.QueryRowContext(ctx, q, versionID)
	}

	var data []byte
	if err := row.Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: snapshot %s not found", versionID)
		}
		return nil, fmt.Errorf("store: get snapshot: %w", err)
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

// upsertSnapshotBlob is the dedup-aware blob insert path used by
// AppendVersion (spec §3 #1, #4). It performs:
//
//  1. INSERT OR IGNORE INTO snapshot_blobs (hash, data, refcount=1).
//     If the row is newly inserted, RowsAffected==1 and we're done.
//  2. Otherwise the hash already exists — read the existing data
//     and compare bytes to incoming. Mismatch = ErrHashCollision.
//  3. Match: bump refcount with an overflow guard
//     (refcount < INT64_MAX). If the guard rejects the update,
//     return ErrRefcountOverflow.
//
// The shared *sql.Tx caller controls atomicity; on error this
// helper returns and the caller's tx is rolled back.
func upsertSnapshotBlob(ctx context.Context, tx sqlExec, hash string, data json.RawMessage) error {
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO snapshot_blobs (hash, data, refcount) VALUES (?, ?, 1)`,
		hash, []byte(data),
	)
	if err != nil {
		return fmt.Errorf("store: append version: insert snapshot_blob: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: append version: rows affected: %w", err)
	}
	if n == 1 {
		// Fresh blob — refcount=1, nothing else to do.
		return nil
	}

	// Existing row: integrity check before bumping refcount.
	var existing []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT data FROM snapshot_blobs WHERE hash = ?`, hash,
	).Scan(&existing); err != nil {
		return fmt.Errorf("store: append version: read existing snapshot_blob: %w", err)
	}
	if !bytes.Equal(existing, []byte(data)) {
		return ErrHashCollision
	}

	// Bump refcount with overflow guard. The WHERE clause prevents
	// silent wrap if the row sits at INT64_MAX (decision #4).
	bumped, err := tx.ExecContext(ctx,
		`UPDATE snapshot_blobs SET refcount = refcount + 1 WHERE hash = ? AND refcount < ?`,
		hash, int64(math.MaxInt64),
	)
	if err != nil {
		return fmt.Errorf("store: append version: bump refcount: %w", err)
	}
	bn, err := bumped.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: append version: bump rows affected: %w", err)
	}
	if bn == 0 {
		return ErrRefcountOverflow
	}
	return nil
}

// DeleteHistory implements VersionStore. Relies on FK ON DELETE
// CASCADE for versions → version_parents and version_snapshots,
// then decrements snapshot_blobs.refcount per affected hash and
// deletes blob rows whose count reaches zero (spec §3 #5).
func (s *sqliteVersionStore) DeleteHistory(ctx context.Context, docType, id string) error {
	conn, commit, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("store: delete history: begin: %w", err)
	}
	derr := s.deleteHistoryTx(ctx, conn, docType, id)
	if cerr := commit(derr); cerr != nil {
		if derr != nil {
			return derr
		}
		return fmt.Errorf("store: delete history: commit: %w", cerr)
	}
	return derr
}

func (s *sqliteVersionStore) deleteHistoryTx(ctx context.Context, tx sqlExec, docType, id string) error {
	// 1. Snapshot the set of hashes this document references BEFORE
	//    deleting the versions rows. We need them for the per-blob
	//    refcount decrement and the delete-on-zero pass; once the
	//    versions row goes the FK cascade evicts version_snapshots
	//    too and we lose the indirection.
	//
	//    A hash may appear multiple times across this doc's
	//    versions (sibling Forks, Reverts that re-snapshot the same
	//    payload, etc); we count occurrences so a single
	//    SQL UPDATE per distinct hash decrements by the correct
	//    delta in one shot. This keeps the refcount integrity
	//    invariant simple and avoids N round trips for a doc with
	//    N versions.
	hashCounts, err := snapshotHashesForDoc(ctx, tx, docType, id)
	if err != nil {
		return fmt.Errorf("store: delete history: collect hashes: %w", err)
	}

	// 2. Delete the versions rows. FK ON DELETE CASCADE removes
	//    matching version_parents and version_snapshots rows.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM versions WHERE type = ? AND id = ?`, docType, id,
	); err != nil {
		return fmt.Errorf("store: delete history: %w", err)
	}

	// 2a. Drop the high-water seq counter for this (type, id). The
	//     document is going away — any future AppendVersion under
	//     the same key is a fresh document that should restart at
	//     seq=1. Mirrors the in-memory backend's
	//     delete(s.nextSeq, key). DeleteVersions/Prune does NOT do
	//     this (that's the load-bearing monotonicity invariant).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM version_seq_high_water WHERE type = ? AND id = ?`, docType, id,
	); err != nil {
		return fmt.Errorf("store: delete history: clear high-water: %w", err)
	}

	// 3. Per affected hash: decrement refcount by the count of
	//    references this document held; surface
	//    ErrRefcountUnderflow if the result would dip below zero
	//    (defense-in-depth against the version_snapshots/
	//    snapshot_blobs join becoming inconsistent — the SQL CHECK
	//    constraint enforces the same invariant at the storage
	//    layer, but logging + an explicit error makes the bug
	//    diagnosable instead of opaque).
	//
	//    Then delete blob rows that reached refcount=0. SQLite
	//    won't run our refcount-decrement trigger from the FK
	//    cascade above (spec §4: refcount logic stays in code so
	//    the in-memory backend can mirror it without triggers).
	for hash, n := range hashCounts {
		if err := decrementSnapshotBlob(ctx, tx, hash, n); err != nil {
			return err
		}
	}

	s.mu.Lock()
	delete(s.dagCache, docKey(docType, id))
	s.dagGen[docKey(docType, id)]++
	s.mu.Unlock()
	return nil
}

// snapshotHashesForDoc returns a hash → count map of the snapshot
// hashes referenced by every version of (docType, id). Used by
// DeleteHistory to apply the right refcount delta per blob.
func snapshotHashesForDoc(ctx context.Context, tx sqlExec, docType, id string) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT vs.hash
		 FROM version_snapshots vs
		 JOIN versions v ON v.version_id = vs.version_id
		 WHERE v.type = ? AND v.id = ?`,
		docType, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h]++
	}
	return out, rows.Err()
}

// decrementSnapshotBlob drops `delta` from the named blob's refcount
// and deletes the row when the count reaches zero. Returns
// ErrRefcountUnderflow if the new value would be negative.
func decrementSnapshotBlob(ctx context.Context, tx sqlExec, hash string, delta int64) error {
	// Read current refcount so we can validate the decrement before
	// applying it. The CHECK (refcount >= 0) constraint will reject
	// an underflow at the SQL layer, but reading first lets us
	// surface the dedicated sentinel + log message without a less
	// informative constraint-violation error from the driver.
	var current int64
	err := tx.QueryRowContext(ctx,
		`SELECT refcount FROM snapshot_blobs WHERE hash = ?`, hash,
	).Scan(&current)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("store: delete history: refcount underflow for hash=%s (blob row missing)", hash)
			return fmt.Errorf("%w: hash=%s blob row missing", ErrRefcountUnderflow, hash)
		}
		return fmt.Errorf("store: delete history: read refcount %s: %w", hash, err)
	}
	if current < delta {
		log.Printf("store: delete history: refcount underflow for hash=%s (current=%d, delta=%d)", hash, current, delta)
		return fmt.Errorf("%w: hash=%s current=%d delta=%d", ErrRefcountUnderflow, hash, current, delta)
	}

	if current == delta {
		// Last reference — delete the blob row outright.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM snapshot_blobs WHERE hash = ?`, hash,
		); err != nil {
			return fmt.Errorf("store: delete history: delete blob %s: %w", hash, err)
		}
		return nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE snapshot_blobs SET refcount = refcount - ? WHERE hash = ?`, delta, hash,
	); err != nil {
		return fmt.Errorf("store: delete history: decrement %s: %w", hash, err)
	}
	return nil
}

// DeleteVersions implements VersionStore for SQLite. Removes the
// named versions for (docType, id) inside a BEGIN IMMEDIATE
// transaction:
//
//  1. Snapshot the hash + bytes for each doomed version (we need
//     these AFTER the rows go to populate FreedBlob; once cascaded,
//     version_snapshots → snapshot_blobs is no longer reachable).
//  2. DELETE FROM versions WHERE version_id IN (?, ...) — FK CASCADE
//     removes matching version_parents (child side) and
//     version_snapshots rows.
//  3. DELETE FROM version_parents WHERE parent_id IN (?, ...) — the
//     parent_id side does NOT cascade (the FK only cascades on the
//     child); we clean it explicitly so no orphan parent edges
//     survive.
//  4. For each freed hash, count occurrences in the doomed set,
//     call decrementSnapshotBlob with that delta. The helper deletes
//     the blob row when refcount hits zero.
//  5. Build the FreedBlob slice from the hashes whose post-decrement
//     refcount is zero (i.e., the row no longer exists). The bytes
//     come from the pre-delete snapshot we took in step 1.
//
// Empty versionIDs is a no-op success.
func (s *sqliteVersionStore) DeleteVersions(ctx context.Context, docType, id string, versionIDs []string) ([]FreedBlob, error) {
	if len(versionIDs) == 0 {
		return nil, nil
	}

	conn, commit, err := beginImmediate(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("store: prune: begin: %w", err)
	}

	freed, derr := s.deleteVersionsTx(ctx, conn, docType, id, versionIDs)
	if cerr := commit(derr); cerr != nil {
		if derr != nil {
			return nil, derr
		}
		return nil, fmt.Errorf("store: prune: commit: %w", cerr)
	}
	return freed, derr
}

// deleteVersionsTx is the body of DeleteVersions, factored out so
// the tx boundary is explicit. Caller owns commit/rollback.
func (s *sqliteVersionStore) deleteVersionsTx(ctx context.Context, tx sqlExec, docType, id string, versionIDs []string) ([]FreedBlob, error) {
	// 1. Per-doomed-version: read the hash and the blob's current
	//    byte size. We need the bytes for FreedBlob and the hash for
	//    the per-blob refcount-decrement pass below. Counting hashes
	//    here gives the correct delta even when multiple doomed
	//    versions share a hash (e.g. Fork sibling that shares bytes).
	type doomed struct {
		versionID string
		hash      string
		bytes     int64
	}
	hashCounts := make(map[string]int64, len(versionIDs))
	hashBytes := make(map[string]int64, len(versionIDs))

	args := make([]any, 0, len(versionIDs))
	for _, vid := range versionIDs {
		args = append(args, vid)
	}
	placeholders := buildPlaceholders(len(versionIDs))

	hashQ := fmt.Sprintf(
		`SELECT vs.version_id, vs.hash, LENGTH(b.data)
		 FROM version_snapshots vs
		 JOIN snapshot_blobs   b  ON b.hash = vs.hash
		 JOIN versions         v  ON v.version_id = vs.version_id
		 WHERE v.type = ? AND v.id = ? AND vs.version_id IN (%s)`,
		placeholders,
	)
	hashArgs := append([]any{docType, id}, args...)
	rows, err := tx.QueryContext(ctx, hashQ, hashArgs...)
	if err != nil {
		return nil, fmt.Errorf("store: prune: collect hashes: %w", err)
	}
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.versionID, &d.hash, &d.bytes); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: prune: scan hash: %w", err)
		}
		hashCounts[d.hash]++
		hashBytes[d.hash] = d.bytes
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: prune: iterate hashes: %w", err)
	}

	// 2. Pre-clean version_parents on BOTH sides for the doomed set.
	//    The version_parents schema FKs version_id with ON DELETE
	//    CASCADE but parent_id without. If we DELETE FROM versions
	//    first while a doomed version is still listed as a parent_id
	//    in some retained child's row, the parent_id FK fires and
	//    aborts the delete (SQLite's FK enforcement raises
	//    constraint failed (787) under foreign_keys=ON).
	//
	//    Deleting both sides up front breaks both FK chains for the
	//    doomed rows: child-side rows where version_id ∈ doomed go,
	//    AND parent-side rows where parent_id ∈ doomed go. After
	//    this step, version_parents has no surviving rows that
	//    reference any doomed version_id; the subsequent DELETE FROM
	//    versions can succeed without tripping the constraint.
	//
	//    Under our bottom-up rule the parent-side case shouldn't
	//    happen (a doomed parent's children are also doomed), but the
	//    cleanup is cheap and keeps the implementation defensive.
	delChildSideQ := fmt.Sprintf(
		`DELETE FROM version_parents WHERE version_id IN (%s)`,
		placeholders,
	)
	if _, err := tx.ExecContext(ctx, delChildSideQ, args...); err != nil {
		return nil, fmt.Errorf("store: prune: delete child-side parent edges: %w", err)
	}
	delParentSideQ := fmt.Sprintf(
		`DELETE FROM version_parents WHERE parent_id IN (%s)`,
		placeholders,
	)
	if _, err := tx.ExecContext(ctx, delParentSideQ, args...); err != nil {
		return nil, fmt.Errorf("store: prune: delete parent-side edges: %w", err)
	}

	// 3. DELETE FROM versions — FK CASCADE on version_id removes
	//    matching version_snapshots rows. Scope to (type, id) for
	//    safety so a versionID collision across documents (impossible
	//    by spec but cheap to guard) can't strip rows from a sibling
	//    document.
	delVersionsQ := fmt.Sprintf(
		`DELETE FROM versions WHERE type = ? AND id = ? AND version_id IN (%s)`,
		placeholders,
	)
	if _, err := tx.ExecContext(ctx, delVersionsQ, hashArgs...); err != nil {
		return nil, fmt.Errorf("store: prune: delete versions: %w", err)
	}

	// 4. Per affected hash: decrement by the count of doomed-version
	//    references. decrementSnapshotBlob deletes when refcount==0.
	freed := make([]FreedBlob, 0, len(hashCounts))
	for hash, n := range hashCounts {
		if err := decrementSnapshotBlob(ctx, tx, hash, n); err != nil {
			return nil, err
		}
		// Check if the blob row still exists. If not, refcount hit
		// zero and decrementSnapshotBlob deleted it → freed.
		var stillThere int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM snapshot_blobs WHERE hash = ?`, hash,
		).Scan(&stillThere); err != nil {
			return nil, fmt.Errorf("store: prune: check blob survival: %w", err)
		}
		if stillThere == 0 {
			freed = append(freed, FreedBlob{Hash: hash, Bytes: hashBytes[hash]})
		}
	}

	// 5. Invalidate cached DAG. Same protocol as DeleteHistory.
	s.mu.Lock()
	delete(s.dagCache, docKey(docType, id))
	s.dagGen[docKey(docType, id)]++
	s.mu.Unlock()

	return freed, nil
}

// buildPlaceholders returns "?,?,...,?" for an IN clause of size n.
// Caller-allocated string; n must be >= 1 (callers check).
func buildPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, 2*n-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

// SetLive implements VersionStore for SQLite. Validates that
// versionID is a current head (no children in version_parents) then
// runs UPDATE versions SET live = ? WHERE version_id = ? inside a
// BEGIN IMMEDIATE transaction.
//
// Convention: live=true → SQLite stores 1; live=false → 0. The
// versions.live column is NOT NULL DEFAULT 1, so the column always
// has a defined value.
//
// Idempotent (UPDATE to the already-current value is a successful
// no-op — RowsAffected may be 0, which is fine).
//
// Returns ErrNotAHead if versionID has at least one row in
// version_parents with parent_id = versionID (i.e., is somebody's
// parent). Returns a non-nil error if versionID is not known to the
// store.
func (s *sqliteVersionStore) SetLive(ctx context.Context, docType, id, versionID string, live bool) error {
	conn, commit, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("store: set live: begin: %w", err)
	}

	serr := s.setLiveTx(ctx, conn, docType, id, versionID, live)
	if cerr := commit(serr); cerr != nil {
		if serr != nil {
			return serr
		}
		return fmt.Errorf("store: set live: commit: %w", cerr)
	}
	return serr
}

func (s *sqliteVersionStore) setLiveTx(ctx context.Context, tx sqlExec, docType, id, versionID string, live bool) error {
	// 1. Verify the version exists for THIS (type, id). Scoping
	//    matches the appendVersionTx scoping rule and avoids touching
	//    a sibling doc's row on a malformed call.
	var seen int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM versions WHERE version_id = ? AND type = ? AND id = ?`,
		versionID, docType, id,
	).Scan(&seen); err != nil {
		return fmt.Errorf("store: set live: lookup: %w", err)
	}
	if seen == 0 {
		return fmt.Errorf("store: set live: %s not found for %s/%s", versionID, docType, id)
	}

	// 2. Validate it's a head — no rows in version_parents where
	//    parent_id = versionID. The application-layer check mirrors
	//    the in-memory backend's DAG.Children walk so the error
	//    surface is identical across backends.
	var children int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM version_parents WHERE parent_id = ?`, versionID,
	).Scan(&children); err != nil {
		return fmt.Errorf("store: set live: head check: %w", err)
	}
	if children > 0 {
		return fmt.Errorf("%w: %s has %d child(ren)", ErrNotAHead, versionID, children)
	}

	// 3. UPDATE the live bit. INTEGER 1 = true, 0 = false.
	liveVal := 0
	if live {
		liveVal = 1
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE versions SET live = ? WHERE version_id = ?`, liveVal, versionID,
	); err != nil {
		return fmt.Errorf("store: set live: update: %w", err)
	}

	// 4. Invalidate the cached DAG. The DAG itself doesn't carry
	//    Live (live is a versions-table concern), but ListVersions's
	//    callers query both — the cache is per-doc rather than
	//    per-table, so we bump the gen anyway. Cheap.
	s.mu.Lock()
	delete(s.dagCache, docKey(docType, id))
	s.dagGen[docKey(docType, id)]++
	s.mu.Unlock()

	return nil
}

// LoadDAG implements VersionStore. Materializes lazily: the first
// call for (docType, id) reads versions + version_parents and
// builds an in-memory DAG; subsequent calls return the cached DAG
// until DeleteHistory or AppendVersion invalidates it.
//
// Race protocol against concurrent writes:
//
//  1. RLock, check cache. Hit → return cached DAG.
//  2. Miss → read dagGen[key] (the generation at the most recent
//     invalidation), drop RLock, run buildDAG without holding any
//     lock so a slow query doesn't serialize the store.
//  3. WLock to commit. If another LoadDAG already populated the
//     cache (a concurrent miss-builder finished first), return
//     that one — both built from a row snapshot at-or-after the
//     same generation, so observable equivalence holds.
//  4. If dagGen[key] has moved, our build saw a snapshot taken
//     before the most recent invalidation; the freshly-committed
//     write isn't reflected. DO NOT cache the stale DAG; return
//     it (the caller asked for *a* DAG; the next LoadDAG will
//     rebuild from current rows).
//  5. Otherwise the cache is empty and the generation matches
//     ours: store and return.
func (s *sqliteVersionStore) LoadDAG(ctx context.Context, docType, id string) (*version.DAG, error) {
	key := docKey(docType, id)

	s.mu.RLock()
	if dag, ok := s.dagCache[key]; ok {
		s.mu.RUnlock()
		return dag, nil
	}
	gen := s.dagGen[key]
	s.mu.RUnlock()

	dag, err := s.buildDAG(ctx, docType, id)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.dagCache[key]; ok {
		// A concurrent LoadDAG already populated the cache.
		// Return its DAG to keep callers consistent.
		return cached, nil
	}
	if s.dagGen[key] != gen {
		// A concurrent write invalidated after we read the
		// generation; our DAG is from a pre-write snapshot.
		// Return without caching so the next LoadDAG rebuilds.
		return dag, nil
	}
	s.dagCache[key] = dag
	return dag, nil
}

func (s *sqliteVersionStore) buildDAG(ctx context.Context, docType, id string) (*version.DAG, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT version_id, hash, timestamp FROM versions WHERE type = ? AND id = ? ORDER BY seq`,
		docType, id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: load dag: versions: %w", err)
	}
	defer rows.Close()

	type row struct {
		ID   string
		Hash string
		TS   int64
	}
	var ordered []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Hash, &r.TS); err != nil {
			return nil, fmt.Errorf("store: load dag: scan: %w", err)
		}
		ordered = append(ordered, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: load dag: rows: %w", err)
	}

	// Bulk-load parents in a second query keyed on this document's
	// version IDs. Avoids the N+1 query pattern at the cost of one
	// extra round trip.
	parents := make(map[string][]string, len(ordered))
	if len(ordered) > 0 {
		ids := make([]any, len(ordered))
		for i, r := range ordered {
			ids[i] = r.ID
		}
		// Build an IN-clause with ?-placeholders; bounded by the
		// number of versions for this document.
		placeholders := ""
		for i := range ids {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
		}
		// ORDER BY rowid recovers insertion order, which is the only
		// way to preserve the [sourceVersionID, targetVersionID]
		// ordering Merge guarantees per spec §4. The PRIMARY KEY
		// (version_id, parent_id) sorts lexicographically by
		// parent_id; without an explicit ORDER BY rowid the SQLite
		// planner returns rows in whatever scan order it picks,
		// which is not insertion order. rowid is intrinsic to
		// non-WITHOUT-ROWID tables, so this needs no schema change
		// (decision #8 holds).
		q := fmt.Sprintf(
			`SELECT version_id, parent_id FROM version_parents WHERE version_id IN (%s) ORDER BY rowid`,
			placeholders,
		)
		prows, err := s.db.QueryContext(ctx, q, ids...)
		if err != nil {
			return nil, fmt.Errorf("store: load dag: parents: %w", err)
		}
		for prows.Next() {
			var vid, pid string
			if err := prows.Scan(&vid, &pid); err != nil {
				prows.Close()
				return nil, fmt.Errorf("store: load dag: parents scan: %w", err)
			}
			parents[vid] = append(parents[vid], pid)
		}
		prows.Close()
	}

	dag := version.NewDAG()
	for _, r := range ordered {
		if err := dag.Append(version.Version{
			ID:        r.ID,
			ParentIDs: parents[r.ID],
			Timestamp: r.TS,
			Hash:      r.Hash,
		}); err != nil {
			return nil, fmt.Errorf("store: load dag: append %s: %w", r.ID, err)
		}
	}
	return dag, nil
}
