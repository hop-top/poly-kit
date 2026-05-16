// Package tidb provides a SchemaDriver for TiDB (MySQL-compatible) databases.
package tidb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	_ "github.com/go-sql-driver/mysql"

	"hop.top/kit/go/core/upgrade"
)

var validTable = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func validateTable(name string) error {
	if !validTable.MatchString(name) {
		return fmt.Errorf("invalid table name: %q", name)
	}
	return nil
}

// Driver implements upgrade.SchemaDriver using a TiDB/MySQL table to track
// the current schema version.
type Driver struct {
	db    *sql.DB
	name  string
	table string
}

// New opens a TiDB/MySQL connection and ensures the version table exists.
func New(dsn string, table string, opts ...Option) (*Driver, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("tidb upgrade: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("tidb upgrade: ping: %w", err)
	}

	d := &Driver{
		db:    db,
		name:  "tidb",
		table: table,
	}
	for _, o := range opts {
		o(d)
	}

	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		schema_name VARCHAR(255) PRIMARY KEY,
		version VARCHAR(255) NOT NULL DEFAULT ''
	)`, d.table)
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("tidb upgrade: migrate: %w", err)
	}

	return d, nil
}

// Option configures a TiDB driver.
type Option func(*Driver)

// WithName overrides the schema name (default "tidb").
func WithName(name string) Option {
	return func(d *Driver) { d.name = name }
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Version() (string, error) {
	q := fmt.Sprintf(
		`SELECT version FROM %s WHERE schema_name = ?`, d.table)
	var v string
	err := d.db.QueryRow(q, d.name).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tidb upgrade: version: %w", err)
	}
	return v, nil
}

func (d *Driver) SetVersion(version string) error {
	q := fmt.Sprintf(
		`INSERT INTO %s (schema_name, version) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE version = VALUES(version)`,
		d.table)
	_, err := d.db.ExecContext(context.Background(), q, d.name, version)
	if err != nil {
		return fmt.Errorf("tidb upgrade: set version: %w", err)
	}
	return nil
}

// Backup is a no-op; TiDB backup is managed externally.
func (d *Driver) Backup(dest string) error { return nil }

// Restore is a no-op; TiDB restore is managed externally.
func (d *Driver) Restore(src string) error { return nil }

// Close releases the underlying database connection.
func (d *Driver) Close() error { return d.db.Close() }

var _ upgrade.SchemaDriver = (*Driver)(nil)
