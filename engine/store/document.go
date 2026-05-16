package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"hop.top/kit/go/core/util"
	"hop.top/kit/go/storage/sqldb"
)

// Document is a type-tagged JSON blob stored in SQLite.
type Document struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// GetID satisfies domain.Entity.
func (d Document) GetID() string { return d.ID }

// Query holds list/search parameters.
type Query struct {
	Limit  int
	Offset int
	Sort   string
	Search string
}

// DocumentStore manages typed JSON documents in a single SQLite table.
type DocumentStore struct {
	db *sql.DB
}

const createTableSQL = `CREATE TABLE IF NOT EXISTS documents (
	type       TEXT NOT NULL,
	id         TEXT NOT NULL,
	data       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (type, id)
)`

// versionTablesSQL is the additive migration for the SQLite-backed
// VersionStore (spec §5). Created unconditionally on every boot so
// the schema is present whether or not the SQLite version backend
// is wired — safe because empty tables cost nothing and CREATE ...
// IF NOT EXISTS is idempotent.
//
// Schema shape per `engine-snapshot-dedup` §4: snapshots are stored
// content-addressed in `snapshot_blobs(hash, data, refcount)` with
// a per-version join `version_snapshots(version_id, hash)`. The old
// `snapshots(version_id, data)` table from the original
// `engine-versioned-sqlite` track is replaced; an idempotent
// migration in [migrateToDedup] walks any legacy rows and folds
// them into the new shape on first boot of an upgraded install.
const versionTablesSQL = `
CREATE TABLE IF NOT EXISTS versions (
	type        TEXT    NOT NULL,
	id          TEXT    NOT NULL,
	version_id  TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	hash        TEXT    NOT NULL,
	timestamp   INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	live        INTEGER NOT NULL DEFAULT 1,
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
CREATE INDEX IF NOT EXISTS idx_version_parents_child ON version_parents(version_id);

CREATE TABLE IF NOT EXISTS snapshot_blobs (
	hash      TEXT    NOT NULL PRIMARY KEY,
	data      BLOB    NOT NULL,
	refcount  INTEGER NOT NULL CHECK (refcount >= 0)
);

CREATE TABLE IF NOT EXISTS version_snapshots (
	version_id  TEXT NOT NULL PRIMARY KEY,
	hash        TEXT NOT NULL,
	FOREIGN KEY (version_id) REFERENCES versions(version_id) ON DELETE CASCADE,
	FOREIGN KEY (hash)       REFERENCES snapshot_blobs(hash)
);
CREATE INDEX IF NOT EXISTS idx_version_snapshots_hash ON version_snapshots(hash);

-- version_seq_high_water tracks the highest seq ever issued per
-- (type, id). Load-bearing for monotonic seq across Prune. Stored
-- separately from the versions table so DELETE FROM versions during
-- DeleteVersions / Prune cannot drop the row that holds the max,
-- which would otherwise let MAX(seq)+1 reissue an already-used seq
-- (and so collide via util.Short on an already-used version_id).
-- Mirrors the in-memory backend's nextSeq map. DeleteHistory clears
-- the row so a fresh document for the same (type, id) restarts at
-- seq=1.
CREATE TABLE IF NOT EXISTS version_seq_high_water (
	type      TEXT    NOT NULL,
	id        TEXT    NOT NULL,
	next_seq  INTEGER NOT NULL,
	PRIMARY KEY (type, id)
);
`

// NewDocumentStore opens (or creates) an SQLite DB at dbPath and
// ensures the documents table plus the additive version tables
// (versions, version_parents, snapshot_blobs, version_snapshots)
// exist. The version tables are created even when callers wire only
// the in-memory VersionStore — they are unused but cost nothing,
// and ensure switching backends never requires a separate migration
// step.
//
// On boot, [migrateToDedup] runs a one-shot idempotent migration
// that folds any legacy `snapshots(version_id, data)` rows from a
// pre-`engine-snapshot-dedup` install into the content-addressed
// `snapshot_blobs` + `version_snapshots` shape. After the migration
// the legacy table is dropped. Re-running on an already-migrated DB
// is a no-op.
func NewDocumentStore(dbPath string) (*DocumentStore, error) {
	db, err := sqldb.Open(sqldb.Options{Path: dbPath})
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}
	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create table: %w", err)
	}
	if _, err := db.Exec(versionTablesSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create version tables: %w", err)
	}
	if err := migrateToDedup(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: dedup migration: %w", err)
	}
	if err := migrateAddLiveColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: live-column migration: %w", err)
	}
	return &DocumentStore{db: db}, nil
}

// migrateAddLiveColumn is the additive migration for the live/dead
// head model (engine-version-pruning decision #10). It is idempotent:
// on a fresh DB the `live` column is already present (CREATE TABLE
// in versionTablesSQL includes it); on a legacy DB (predating this
// track) we add it via ALTER TABLE with DEFAULT 1, which both
// satisfies the NOT NULL constraint for existing rows and matches
// the "absent or true means live" convention the in-memory backend
// uses (newly-appended versions are born live).
//
// The check uses `PRAGMA table_info(versions)` and walks the result
// for a row named `live`; if absent, run the ALTER. Re-running on a
// post-migration DB sees the column and is a no-op.
//
// SQLite restriction: ALTER TABLE ADD COLUMN with a constant DEFAULT
// is supported and rewrites no existing data — it just shifts the
// row format. The DEFAULT 1 lands on every pre-existing row, which
// is what we want.
func migrateAddLiveColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(versions)`)
	if err != nil {
		return fmt.Errorf("check versions schema: %w", err)
	}
	defer rows.Close()

	hasLive := false
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
		// We only care about the name.
		var (
			cid       int
			name      string
			colType   string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan versions schema: %w", err)
		}
		if name == "live" {
			hasLive = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate versions schema: %w", err)
	}
	if hasLive {
		return nil
	}

	if _, err := db.Exec(`ALTER TABLE versions ADD COLUMN live INTEGER NOT NULL DEFAULT 1`); err != nil {
		return fmt.Errorf("alter table versions add live: %w", err)
	}
	return nil
}

// migrateToDedup folds any legacy `snapshots(version_id, data)` rows
// from a pre-`engine-snapshot-dedup` install into the new
// content-addressed shape. The migration runs in a single
// transaction so a crash mid-walk leaves the DB in the
// pre-migration state, recoverable on the next boot. It is
// idempotent: when the legacy table is already absent (fresh boot
// or post-migration) the function returns immediately.
//
// Per spec §6: each legacy row is hashed via util.Short(data, 16),
// inserted into snapshot_blobs (deduping by hash with refcount
// aggregation via ON CONFLICT), and joined through
// version_snapshots. The legacy table is dropped at the end.
func migrateToDedup(db *sql.DB) error {
	// Idempotency check: only proceed if the legacy `snapshots`
	// table still exists. The new tables (snapshot_blobs and
	// version_snapshots) are always created by versionTablesSQL.
	var legacyName string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='snapshots'`,
	).Scan(&legacyName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("check legacy snapshots: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best effort if commit fails

	rows, err := tx.Query(`SELECT version_id, data FROM snapshots`)
	if err != nil {
		return fmt.Errorf("scan legacy: %w", err)
	}
	type legacyRow struct {
		versionID string
		data      []byte
	}
	var legacy []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.versionID, &r.data); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy row: %w", err)
		}
		legacy = append(legacy, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy: %w", err)
	}

	// Walk each row: hash, upsert into snapshot_blobs with
	// refcount aggregation, insert join row.
	for _, r := range legacy {
		h := util.Short(r.data, 16)
		if _, err := tx.Exec(
			`INSERT INTO snapshot_blobs (hash, data, refcount) VALUES (?, ?, 1)
			 ON CONFLICT(hash) DO UPDATE SET refcount = refcount + 1`,
			h, r.data,
		); err != nil {
			return fmt.Errorf("upsert snapshot_blobs: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO version_snapshots (version_id, hash) VALUES (?, ?)`,
			r.versionID, h,
		); err != nil {
			return fmt.Errorf("insert version_snapshots: %w", err)
		}
	}

	// Drop the legacy table inside the same tx so a partial
	// migration is impossible.
	if _, err := tx.Exec(`DROP TABLE snapshots`); err != nil {
		return fmt.Errorf("drop legacy: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// DB exposes the underlying *sql.DB so the SQLite VersionStore can
// share the same connection (and therefore the same transaction
// boundary) as document writes. Callers MUST treat this as
// read-only access to the connection — the DocumentStore owns the
// lifecycle and will Close it.
func (s *DocumentStore) DB() *sql.DB { return s.db }

// Create inserts a new document. If the JSON data contains an "id"
// field it is used; otherwise one is generated via util.Short.
func (s *DocumentStore) Create(ctx context.Context, docType string, data json.RawMessage) (Document, error) {
	return s.createExec(ctx, s.db, docType, data)
}

// createConn is the conn-aware variant of Create. Used by the
// shared-tx path when the caller has checked out a *sql.Conn and
// driven a BEGIN IMMEDIATE on it (see versioned.go's
// beginImmediate). Functionally identical to createTx; the
// distinction lives at the type level only.
func (s *DocumentStore) createConn(ctx context.Context, conn *sql.Conn, docType string, data json.RawMessage) (Document, error) {
	return s.createExec(ctx, conn, docType, data)
}

// execer is the subset of *sql.DB / *sql.Tx that Create/Update/
// Delete need. Lets us share one body across the standalone and
// tx-aware variants without leaking sql.Tx into the public API.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *DocumentStore) createExec(ctx context.Context, e execer, docType string, data json.RawMessage) (Document, error) {
	id := extractID(data)
	if id == "" {
		id = util.Short([]byte(fmt.Sprintf("%s-%d", docType, time.Now().UnixNano())), 12)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	doc := Document{
		Type:      docType,
		ID:        id,
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := e.ExecContext(ctx,
		`INSERT INTO documents (type, id, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		doc.Type, doc.ID, string(doc.Data), doc.CreatedAt, doc.UpdatedAt,
	)
	if err != nil {
		return Document{}, fmt.Errorf("store: create: %w", err)
	}
	return doc, nil
}

// Get retrieves a single document by type and ID.
func (s *DocumentStore) Get(ctx context.Context, docType, id string) (Document, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT type, id, data, created_at, updated_at FROM documents WHERE type = ? AND id = ?`,
		docType, id,
	)
	return scanDocument(row)
}

// List returns documents of the given type matching the query.
func (s *DocumentStore) List(ctx context.Context, docType string, q Query) ([]Document, error) {
	if q.Limit == 0 {
		q.Limit = 100
	}

	query := `SELECT type, id, data, created_at, updated_at FROM documents WHERE type = ?`
	args := []any{docType}

	if q.Search != "" {
		query += ` AND data LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLIKE(q.Search)+"%")
	}

	switch q.Sort {
	case "created_at", "updated_at", "id":
		query += ` ORDER BY ` + q.Sort
	default:
		query += ` ORDER BY created_at`
	}

	query += ` LIMIT ?`
	args = append(args, q.Limit)
	if q.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, q.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var data string
		if err := rows.Scan(&d.Type, &d.ID, &data, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		d.Data = json.RawMessage(data)
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// Update replaces the data for an existing document.
func (s *DocumentStore) Update(ctx context.Context, docType, id string, data json.RawMessage) (Document, error) {
	if err := s.updateExec(ctx, s.db, docType, id, data); err != nil {
		return Document{}, err
	}
	return s.Get(ctx, docType, id)
}

// updateConn is the conn-aware variant of Update for the shared-tx
// path (see createConn). Reads the post-write row through the same
// connection so the caller sees its own pending writes.
func (s *DocumentStore) updateConn(ctx context.Context, conn *sql.Conn, docType, id string, data json.RawMessage) (Document, error) {
	if err := s.updateExec(ctx, conn, docType, id, data); err != nil {
		return Document{}, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT type, id, data, created_at, updated_at FROM documents WHERE type = ? AND id = ?`,
		docType, id,
	)
	return scanDocument(row)
}

func (s *DocumentStore) updateExec(ctx context.Context, e execer, docType, id string, data json.RawMessage) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := e.ExecContext(ctx,
		`UPDATE documents SET data = ?, updated_at = ? WHERE type = ? AND id = ?`,
		string(data), now, docType, id,
	)
	if err != nil {
		return fmt.Errorf("store: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("store: update: not found")
	}
	return nil
}

// Delete removes a document by type and ID.
func (s *DocumentStore) Delete(ctx context.Context, docType, id string) error {
	return s.deleteExec(ctx, s.db, docType, id)
}

// deleteConn is the conn-aware variant of Delete for the shared-tx
// path (see createConn).
func (s *DocumentStore) deleteConn(ctx context.Context, conn *sql.Conn, docType, id string) error {
	return s.deleteExec(ctx, conn, docType, id)
}

func (s *DocumentStore) deleteExec(ctx context.Context, e execer, docType, id string) error {
	res, err := e.ExecContext(ctx,
		`DELETE FROM documents WHERE type = ? AND id = ?`,
		docType, id,
	)
	if err != nil {
		return fmt.Errorf("store: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("store: delete: not found")
	}
	return nil
}

// Close closes the underlying database connection.
func (s *DocumentStore) Close() error {
	return s.db.Close()
}

func escapeLIKE(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func extractID(data json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	raw, ok := m["id"]
	if !ok {
		return ""
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return ""
	}
	return id
}

func scanDocument(row *sql.Row) (Document, error) {
	var d Document
	var data string
	if err := row.Scan(&d.Type, &d.ID, &data, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Document{}, fmt.Errorf("store: not found")
		}
		return Document{}, fmt.Errorf("store: scan: %w", err)
	}
	d.Data = json.RawMessage(data)
	return d, nil
}
