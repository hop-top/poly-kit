package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"hop.top/kit/go/storage/sqldb"
)

// Store implements kv.Store and kv.TTLStore backed by SQLite.
type Store struct {
	db     *sql.DB
	writes atomic.Int64
}

// New opens (or creates) a SQLite database at path and ensures the kv table exists.
func New(path string) (*Store, error) {
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("sqlite kv: open: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		key   TEXT PRIMARY KEY,
		value BLOB NOT NULL,
		expires_at INTEGER
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite kv: migrate: %w", err)
	}
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_kv_expires ON kv(expires_at) WHERE expires_at IS NOT NULL`,
	); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite kv: index: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?, ?, NULL)`,
		key, value)
	if err == nil {
		s.maybeSweep(ctx)
	}
	return err
}

func (s *Store) PutWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	exp := time.Now().Add(ttl).UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?, ?, ?)`,
		key, value, exp)
	if err == nil {
		s.maybeSweep(ctx)
	}
	return err
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE key = ? AND (expires_at IS NULL OR expires_at > ?)`,
		key, time.Now().UnixMilli())
	var val []byte
	if err := row.Scan(&val); err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, key)
	return err
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var rows *sql.Rows
	var err error
	now := time.Now().UnixMilli()
	if prefix == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT key FROM kv WHERE (expires_at IS NULL OR expires_at > ?)`, now)
	} else if end := prefixEnd(prefix); end == "" {
		// All-0xff prefix: no upper bound, match everything >= prefix.
		rows, err = s.db.QueryContext(ctx,
			`SELECT key FROM kv WHERE key >= ? AND (expires_at IS NULL OR expires_at > ?)`,
			prefix, now)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT key FROM kv WHERE key >= ? AND key < ? AND (expires_at IS NULL OR expires_at > ?)`,
			prefix, end, now)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) maybeSweep(ctx context.Context) {
	if s.writes.Add(1)%100 == 0 {
		_, _ = s.db.ExecContext(ctx,
			"DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= ?",
			time.Now().UnixMilli())
	}
}

func prefixEnd(p string) string {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1])
		}
	}
	return "" // all 0xFF — match everything
}
