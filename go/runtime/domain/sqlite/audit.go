package sqlite

import (
	"context"
	"database/sql"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/storage/sqlstore"
)

const auditMigrateSQL = `CREATE TABLE IF NOT EXISTS audit_entries (
	entity_id TEXT NOT NULL,
	timestamp TEXT NOT NULL,
	by        TEXT NOT NULL DEFAULT '',
	action    TEXT NOT NULL,
	note      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_entries(entity_id);`

// SQLiteAuditRepository implements domain.AuditRepository backed by sqlstore.
type SQLiteAuditRepository struct {
	db *sql.DB
}

// NewSQLiteAuditRepository creates an audit repository using the given store's
// DB connection. Call CreateTable to ensure the schema exists.
func NewSQLiteAuditRepository(store *sqlstore.Store) *SQLiteAuditRepository {
	return &SQLiteAuditRepository{db: store.DB()}
}

// CreateTable ensures the audit_entries table and index exist.
func (r *SQLiteAuditRepository) CreateTable(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, auditMigrateSQL)
	return err
}

// AddEntry inserts a new audit entry.
func (r *SQLiteAuditRepository) AddEntry(ctx context.Context, entry *domain.AuditEntry) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_entries (entity_id, timestamp, by, action, note)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.EntityID, entry.Timestamp, entry.By, entry.Action, entry.Note,
	)
	return err
}

// ListEntries returns all audit entries for the given entity, ordered by
// timestamp ascending.
func (r *SQLiteAuditRepository) ListEntries(
	ctx context.Context, entityID string,
) ([]*domain.AuditEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT entity_id, timestamp, by, action, note
		 FROM audit_entries WHERE entity_id = ? ORDER BY timestamp ASC`,
		entityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*domain.AuditEntry
	for rows.Next() {
		e := &domain.AuditEntry{}
		if err := rows.Scan(&e.EntityID, &e.Timestamp, &e.By, &e.Action, &e.Note); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
