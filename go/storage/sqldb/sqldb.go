// Package sqldb provides shared SQLite connection management with
// sensible defaults (WAL mode, busy timeout, foreign keys).
package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

var validTable = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func validateTable(name string) error {
	if !validTable.MatchString(name) {
		return fmt.Errorf("invalid table name: %q", name)
	}
	return nil
}

// Options configures the SQLite connection.
type Options struct {
	// Path to the database file. Use ":memory:" for in-memory.
	Path string

	// WAL enables WAL journal mode. Default true.
	WAL *bool

	// BusyTimeout in milliseconds. Default 5000.
	BusyTimeout int
}

func (o Options) wal() bool {
	if o.WAL == nil {
		return true
	}
	return *o.WAL
}

func (o Options) busyTimeout() int {
	if o.BusyTimeout <= 0 {
		return 5000
	}
	return o.BusyTimeout
}

// Open creates or opens a SQLite database with standard pragmas applied.
//
// Pragmas travel via DSN _pragma query parameters so every connection
// the database/sql pool opens inherits them. A previous shape applied
// pragmas with db.Exec after sql.Open, but database/sql may dispatch
// that single statement on any one pooled connection — subsequent
// connections in the pool started fresh without busy_timeout, foreign
// keys or WAL, and returned SQLITE_BUSY (5) immediately under
// contention. The conformance suite (engine/store
// TestVersionStoreConformance/sqlite/ConcurrencySmoke) surfaced this.
// modernc.org/sqlite's _pragma= param applies its arguments inside
// the driver's per-connection Connect path, so every pool entry
// inherits identical PRAGMA state.
func Open(opts Options) (*sql.DB, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("sqldb: path required")
	}

	if opts.Path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
			return nil, fmt.Errorf("sqldb: mkdir: %w", err)
		}
	}

	dsn, err := buildDSN(opts)
	if err != nil {
		return nil, fmt.Errorf("sqldb: build dsn: %w", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqldb: open: %w", err)
	}

	// Smoke-check at least one connection so a misconfigured DSN
	// surfaces here, not at first query.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqldb: ping: %w", err)
	}

	return db, nil
}

// buildDSN assembles the connection string for modernc.org/sqlite,
// appending _pragma= query parameters for every pragma we want every
// pooled connection to inherit. The driver applies _pragma values in
// its per-connection Connect hook (with busy_timeout sorted first),
// so this is the safe way to configure pool-wide PRAGMA state.
//
// The path may already carry query parameters (callers occasionally
// pass DSNs like ":memory:?cache=shared" or "file:foo?mode=ro"); we
// preserve them and append our pragmas alongside.
func buildDSN(opts Options) (string, error) {
	pragmas := []string{
		fmt.Sprintf("busy_timeout=%d", opts.busyTimeout()),
		"foreign_keys=on",
	}
	if opts.wal() {
		pragmas = append(pragmas, "journal_mode=WAL")
	}

	path := opts.Path
	// Split any existing query string off so we can merge cleanly.
	base, query := path, ""
	if i := strings.Index(path, "?"); i >= 0 {
		base, query = path[:i], path[i+1:]
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return "", err
	}
	for _, p := range pragmas {
		values.Add("_pragma", p)
	}

	return base + "?" + values.Encode(), nil
}

// MustOpen is like Open but panics on error.
func MustOpen(opts Options) *sql.DB {
	db, err := Open(opts)
	if err != nil {
		panic(err)
	}
	return db
}

// Migrate applies numbered migrations to the database. It tracks applied
// versions in a table named by the table parameter. Each migration is
// keyed by version number and executed in ascending order. Already-applied
// versions are skipped (idempotent).
func Migrate(db *sql.DB, table string, migrations map[int]string) error {
	if err := validateTable(table); err != nil {
		return err
	}

	create := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		table,
	)
	if _, err := db.Exec(create); err != nil {
		return fmt.Errorf("sqldb: create migrations table: %w", err)
	}

	ctx := context.Background()
	for v := 1; v <= len(migrations); v++ {
		m, ok := migrations[v]
		if !ok {
			continue
		}

		var exists int
		row := db.QueryRow(
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE version = ?", table),
			v,
		)
		if err := row.Scan(&exists); err != nil {
			return fmt.Errorf("sqldb: check version %d: %w", v, err)
		}
		if exists > 0 {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqldb: begin tx v%d: %w", v, err)
		}
		if _, err := tx.Exec(m); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqldb: migrate v%d: %w", v, err)
		}
		if _, err := tx.Exec(
			fmt.Sprintf("INSERT INTO %s (version, applied_at) VALUES (?, datetime('now'))", table),
			v,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqldb: record v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqldb: commit v%d: %w", v, err)
		}
	}

	return nil
}
