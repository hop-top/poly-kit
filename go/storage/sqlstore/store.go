// Package sqlstore provides a generic key-value store backed by SQLite.
//
// Values are JSON-marshaled before storage and unmarshalled on retrieval,
// so any JSON-serialisable type is supported as a value. Keys are plain
// strings with no namespacing; callers are responsible for key uniqueness.
//
// The store creates its own kv table on Open. Callers may add additional
// tables or indexes via Options.MigrateSQL, which is executed in the same
// migration step.
//
// The underlying *sql.DB is accessible via DB() for callers that need to
// execute custom queries against the same connection.
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"hop.top/kit/go/storage/sqldb"
)

// Options controls Store behavior.
type Options struct {
	// TTL, if positive, is the maximum age of a stored value. Get returns
	// (false, nil) for any entry whose stored_at timestamp is older than TTL.
	// A zero or negative TTL disables expiry and all entries are returned
	// regardless of age.
	//
	// Note: expired entries are not deleted from the database by Get. If storage
	// growth is a concern, callers should periodically run:
	//
	//   db.ExecContext(ctx, "delete from kv where stored_at < ?", cutoff)
	TTL time.Duration

	// MigrateSQL, if non-empty, is executed once during Open after the kv
	// table has been created. Use it to add application-specific tables,
	// indexes, or initial data. The SQL is executed as-is via db.Exec, so
	// multiple statements must be separated by semicolons and the driver must
	// support multi-statement exec (modernc.org/sqlite does).
	MigrateSQL string
}

// Store is a key-value store backed by a SQLite database.
// Use Open to create a Store; the zero value is not usable.
// Close must be called when the store is no longer needed.
type Store struct {
	db   *sql.DB
	opts Options
}

// Open opens (or creates) a SQLite database at path, creates the kv table if
// it does not exist, and runs opts.MigrateSQL if provided.
// The parent directory of path is created with mode 0750 if it does not exist.
// The caller must call Close when finished.
func Open(path string, opts Options) (*Store, error) {
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db, opts: opts}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`create table if not exists kv (
	  key       text primary key,
	  stored_at text not null,
	  payload   text not null
	);`)
	if err != nil {
		return err
	}
	if s.opts.MigrateSQL != "" {
		_, err = s.db.Exec(s.opts.MigrateSQL)
	}
	return err
}

// Put serializes v as JSON and upserts it under key.
// If key already exists its value and stored_at timestamp are overwritten.
// v must be JSON-marshallable; an error is returned if marshaling fails.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`insert into kv (key, stored_at, payload) values (?, ?, ?)
		 on conflict(key) do update set stored_at=excluded.stored_at, payload=excluded.payload`,
		key, time.Now().UTC().Format(time.RFC3339), string(data))
	return err
}

// Get retrieves the value stored under key and JSON-unmarshals it into dst.
// Returns (true, nil) on success, (false, nil) if the key does not exist or
// the entry has expired (see Options.TTL), and (false, err) on any other
// failure.
// dst must be a non-nil pointer of a type compatible with the stored JSON.
func (s *Store) Get(ctx context.Context, key string, dst any) (bool, error) {
	var storedAt, payload string
	err := s.db.QueryRowContext(ctx,
		`select stored_at, payload from kv where key = ?`, key).Scan(&storedAt, &payload)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s.opts.TTL > 0 {
		ts, err := time.Parse(time.RFC3339, storedAt)
		if err != nil || time.Since(ts) > s.opts.TTL {
			return false, nil
		}
	}
	return true, json.Unmarshal([]byte(payload), dst)
}

// DB returns the underlying *sql.DB for callers that need to execute custom
// queries. The returned value must not be closed directly; use Close instead.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database connection. It is safe to call Close
// concurrently with in-flight operations; subsequent operations will return
// errors.
func (s *Store) Close() error { return s.db.Close() }
