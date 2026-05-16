// Package sqlite provides a SQLite-backed implementation of ash.Store.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"hop.top/ash"
	"hop.top/kit/go/storage/sqldb"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const timeLayout = time.RFC3339Nano

// Compile-time interface check.
var _ ash.Store = (*Store)(nil)

// Store implements ash.Store backed by SQLite via modernc.org/sqlite.
type Store struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at path and runs
// migrations. Use ":memory:" for an in-memory database.
func New(path string) (*Store, error) {
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}

	// For in-memory databases, restrict to a single connection to
	// prevent data loss across multiple connections.
	if path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		db.SetMaxOpenConns(1)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("sqlite: read migrations: %w", err)
	}
	for _, e := range entries {
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("sqlite: read %s: %w", e.Name(), err)
		}
		if _, err := s.db.Exec(string(data)); err != nil {
			return fmt.Errorf("sqlite: exec %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Create persists a new session from metadata.
func (s *Store) Create(ctx context.Context, meta ash.SessionMeta) error {
	metaJSON, err := json.Marshal(meta.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: marshal metadata: %w", err)
	}

	createdAt := meta.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := meta.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, metadata, parent_id, created_at, updated_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		meta.ID,
		string(metaJSON),
		meta.ParentID,
		createdAt.Format(timeLayout),
		updatedAt.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert session: %w", err)
	}
	return nil
}

// Load retrieves a session and all its turns ordered by seq.
func (s *Store) Load(ctx context.Context, id string) (*ash.Session, error) {
	sess := &ash.Session{}
	var metaStr string
	var createdStr, updatedStr string
	var closedStr sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, metadata, parent_id, created_at, updated_at, closed_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &metaStr, &sess.ParentID, &createdStr,
		&updatedStr, &closedStr)
	if err == sql.ErrNoRows {
		return nil, ash.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: load session: %w", err)
	}

	if err := json.Unmarshal([]byte(metaStr), &sess.Metadata); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal metadata: %w", err)
	}
	var parseErr error
	sess.CreatedAt, parseErr = time.Parse(timeLayout, createdStr)
	if parseErr != nil {
		return nil, fmt.Errorf("sqlite: parse created_at: %w", parseErr)
	}
	sess.UpdatedAt, parseErr = time.Parse(timeLayout, updatedStr)
	if parseErr != nil {
		return nil, fmt.Errorf("sqlite: parse updated_at: %w", parseErr)
	}
	if closedStr.Valid {
		t, parseErr := time.Parse(timeLayout, closedStr.String)
		if parseErr != nil {
			return nil, fmt.Errorf("sqlite: parse closed_at: %w", parseErr)
		}
		sess.ClosedAt = &t
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, content, parts, tool_calls, parent_id,
		        timestamp, metadata, seq
		 FROM turns WHERE session_id = ? ORDER BY seq ASC`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query turns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		sess.Turns = append(sess.Turns, t)
	}
	return sess, rows.Err()
}

// AppendTurn inserts a turn with auto-incremented seq.
func (s *Store) AppendTurn(ctx context.Context, sessionID string, turn ash.Turn) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Verify session exists and is not closed.
	var closedStr sql.NullString
	err = tx.QueryRow(
		`SELECT closed_at FROM sessions WHERE id = ?`, sessionID,
	).Scan(&closedStr)
	if err == sql.ErrNoRows {
		return ash.ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: check session: %w", err)
	}
	if closedStr.Valid {
		return ash.ErrSessionClosed
	}

	// Insert turn with atomically computed seq via subquery.
	if err := insertTurnAutoSeq(tx, sessionID, turn); err != nil {
		return err
	}

	_, err = tx.Exec(
		`UPDATE sessions SET updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(timeLayout), sessionID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update timestamp: %w", err)
	}

	return tx.Commit()
}

// List returns session metadata matching the filter.
func (s *Store) List(ctx context.Context, f ash.Filter) ([]ash.SessionMeta, error) {
	var where []string
	var args []any

	if f.ParentID != "" {
		where = append(where, "s.parent_id = ?")
		args = append(args, f.ParentID)
	}

	zero := time.Time{}
	if f.Before != zero {
		where = append(where, "s.created_at < ?")
		args = append(args, f.Before.Format(timeLayout))
	}
	if f.After != zero {
		where = append(where, "s.created_at > ?")
		args = append(args, f.After.Format(timeLayout))
	}

	query := `SELECT s.id, s.metadata, s.parent_id, s.created_at, s.updated_at,
	                 COUNT(t.id) as turn_count
	          FROM sessions s
	          LEFT JOIN turns t ON t.session_id = s.id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY s.id ORDER BY s.created_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sessions: %w", err)
	}
	defer rows.Close()

	var metas []ash.SessionMeta
	for rows.Next() {
		var m ash.SessionMeta
		var metaStr, createdStr, updatedStr string

		if err := rows.Scan(&m.ID, &metaStr, &m.ParentID,
			&createdStr, &updatedStr, &m.TurnCount); err != nil {
			return nil, fmt.Errorf("sqlite: scan meta: %w", err)
		}

		if err := json.Unmarshal([]byte(metaStr), &m.Metadata); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal meta: %w", err)
		}
		m.CreatedAt, err = time.Parse(timeLayout, createdStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse created_at: %w", err)
		}
		m.UpdatedAt, err = time.Parse(timeLayout, updatedStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse updated_at: %w", err)
		}

		metas = append(metas, m)
	}
	return metas, rows.Err()
}

// Delete removes a session and its turns (cascaded by FK).
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ash.ErrSessionNotFound
	}
	return nil
}

// ListTurns returns turns for the given session matching the filter.
func (s *Store) ListTurns(ctx context.Context, sessionID string, f ash.TurnFilter) ([]ash.Turn, error) {
	q := `SELECT id, role, content, parts, tool_calls, parent_id,
	      timestamp, metadata, seq FROM turns WHERE session_id = ?`
	args := []any{sessionID}

	if f.Role != "" {
		q += ` AND role = ?`
		args = append(args, string(f.Role))
	}
	if !f.After.IsZero() {
		q += ` AND timestamp > ?`
		args = append(args, f.After.Format(time.RFC3339Nano))
	}
	if !f.Before.IsZero() {
		q += ` AND timestamp < ?`
		args = append(args, f.Before.Format(time.RFC3339Nano))
	}
	q += ` ORDER BY seq ASC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	if f.Offset > 0 {
		q += fmt.Sprintf(` OFFSET %d`, f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}
	defer rows.Close()

	var turns []ash.Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	return turns, rows.Err()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func insertTurnAutoSeq(tx *sql.Tx, sessionID string, t ash.Turn) error {
	parts, err := json.Marshal(t.Parts)
	if err != nil {
		return fmt.Errorf("sqlite: marshal parts: %w", err)
	}
	toolCalls, err := json.Marshal(t.ToolCalls)
	if err != nil {
		return fmt.Errorf("sqlite: marshal tool_calls: %w", err)
	}
	meta, err := json.Marshal(t.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: marshal turn metadata: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO turns (id, session_id, role, content, parts,
		                    tool_calls, parent_id, timestamp, metadata, seq)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?,
		         (SELECT COALESCE(MAX(seq), -1) + 1 FROM turns WHERE session_id = ?))`,
		t.ID, sessionID, string(t.Role), t.Content,
		string(parts), string(toolCalls), t.ParentID,
		t.Timestamp.Format(timeLayout), string(meta), sessionID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert turn: %w", err)
	}
	return nil
}

func scanTurn(rows *sql.Rows) (ash.Turn, error) {
	var t ash.Turn
	var roleStr string
	var partsStr, toolCallsStr, metaStr, tsStr string

	err := rows.Scan(
		&t.ID, &roleStr, &t.Content, &partsStr,
		&toolCallsStr, &t.ParentID, &tsStr, &metaStr, &t.Seq,
	)
	if err != nil {
		return t, fmt.Errorf("sqlite: scan turn: %w", err)
	}

	t.Role = ash.Role(roleStr)
	t.Timestamp, err = time.Parse(timeLayout, tsStr)
	if err != nil {
		return t, fmt.Errorf("sqlite: parse turn timestamp: %w", err)
	}

	if err := json.Unmarshal([]byte(partsStr), &t.Parts); err != nil {
		return t, fmt.Errorf("sqlite: unmarshal parts: %w", err)
	}
	if err := json.Unmarshal([]byte(toolCallsStr), &t.ToolCalls); err != nil {
		return t, fmt.Errorf("sqlite: unmarshal tool_calls: %w", err)
	}
	if err := json.Unmarshal([]byte(metaStr), &t.Metadata); err != nil {
		return t, fmt.Errorf("sqlite: unmarshal metadata: %w", err)
	}

	return t, nil
}
