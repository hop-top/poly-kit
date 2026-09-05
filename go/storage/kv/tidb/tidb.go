package tidb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"github.com/go-sql-driver/mysql"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv"
)

var validTable = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func validateTable(name string) error {
	if !validTable.MatchString(name) {
		return fmt.Errorf("invalid table name: %q", name)
	}
	return nil
}

// Store implements kv.Store backed by TiDB (MySQL-compatible).
type Store struct {
	db    *sql.DB
	table string
}

var _ kv.Store = (*Store)(nil)

// New opens a MySQL/TiDB connection and ensures the KV table exists.
//
// It is NewContext with a background context, kept for callers that have
// none to offer. Because the offline marker travels on a context, a Store
// built this way is unguarded at open time; every later query still is,
// since the guard rides the driver's dial hook. Prefer NewContext.
func New(dsn string, table string) (*Store, error) {
	return NewContext(context.Background(), dsn, table)
}

// NewContext opens a MySQL/TiDB connection and ensures the KV table
// exists, honoring the network policy carried by ctx.
//
// The dial is routed through netpolicy.GuardDial via the driver's
// Config.DialFunc, which go-sql-driver/mysql invokes with the context of
// the query that triggered the connection. sql.Open is lazy, so an
// --offline refusal surfaces on the first use — here the ping below, and
// on every subsequent Get/Put/Delete/List — rather than being lost at
// open time.
func NewContext(ctx context.Context, dsn string, table string) (*Store, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("tidb kv: parse dsn: %w", err)
	}
	// ParseDSN never sets DialFunc, so this is the driver's own seam
	// rather than an override of a caller's choice.
	cfg.DialFunc = netpolicy.GuardDial(nil)

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("tidb kv: connector: %w", err)
	}
	db := sql.OpenDB(connector)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("tidb kv: ping: %w", err)
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		k VARCHAR(512) PRIMARY KEY,
		v LONGBLOB NOT NULL
	)`, table)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("tidb kv: migrate: %w", err)
	}
	return &Store{db: db, table: table}, nil
}

func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	q := fmt.Sprintf(
		`INSERT INTO %s (k, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v = VALUES(v)`,
		s.table)
	_, err := s.db.ExecContext(ctx, q, key, value)
	return err
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	q := fmt.Sprintf(`SELECT v FROM %s WHERE k = ?`, s.table)
	row := s.db.QueryRowContext(ctx, q, key)
	var val []byte
	if err := row.Scan(&val); err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	q := fmt.Sprintf(`DELETE FROM %s WHERE k = ?`, s.table)
	_, err := s.db.ExecContext(ctx, q, key)
	return err
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var q string
	var args []interface{}
	if prefix == "" {
		q = fmt.Sprintf(`SELECT k FROM %s`, s.table)
	} else if end := prefixEnd(prefix); end == "" {
		// All-0xff prefix: no upper bound, match everything >= prefix.
		q = fmt.Sprintf(`SELECT k FROM %s WHERE k >= ?`, s.table)
		args = append(args, prefix)
	} else {
		q = fmt.Sprintf(`SELECT k FROM %s WHERE k >= ? AND k < ?`, s.table)
		args = append(args, prefix, end)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// prefixEnd returns the lexicographic successor of prefix for range scans.
// Returns "" when no successor exists (all bytes 0xff).
func prefixEnd(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}
