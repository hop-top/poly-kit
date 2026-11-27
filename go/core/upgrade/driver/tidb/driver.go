// Package tidb provides a SchemaDriver for TiDB (MySQL-compatible) databases.
package tidb

import (
	"context"
	"database/sql"
	"errors"
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
	owned bool // db was opened by New; Close closes it
}

// New opens a TiDB/MySQL connection and ensures the version table exists.
// The driver owns that connection pool and Close closes it. To share the
// application's pool, use NewWithDB.
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

	d := &Driver{db: db, name: "tidb", table: table, owned: true}
	for _, o := range opts {
		o(d)
	}
	if err := d.ensureTable(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// NewWithDB builds a driver on the caller's handle and ensures the version
// table exists. It neither opens nor closes db: Close leaves it open, and
// the caller closes it. Lock takes one extra connection from db for the
// whole run, so db must allow at least two open connections.
func NewWithDB(db *sql.DB, table string, opts ...Option) (*Driver, error) {
	if db == nil {
		return nil, errors.New("tidb upgrade: nil db")
	}
	if err := validateTable(table); err != nil {
		return nil, err
	}
	d := &Driver{db: db, name: "tidb", table: table}
	for _, o := range opts {
		o(d)
	}
	if err := d.ensureTable(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) ensureTable() error {
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		schema_name VARCHAR(255) PRIMARY KEY,
		version VARCHAR(255) NOT NULL DEFAULT ''
	)`, d.table)
	if _, err := d.db.Exec(ddl); err != nil {
		return fmt.Errorf("tidb upgrade: migrate: %w", err)
	}
	return nil
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

// StoresVersion marks the driver as the only authority for its version:
// the Migrator calls SetVersion and writes no version file.
func (d *Driver) StoresVersion() bool { return true }

// Backup reports upgrade.ErrBackupUnsupported: kit does not back up a
// server database, so a run goes on without a backup. Take one outside
// kit before migrating when a failed migration must be recoverable.
func (d *Driver) Backup(dest string) error { return upgrade.ErrBackupUnsupported }

// Restore reports upgrade.ErrBackupUnsupported: nothing is restored, so
// a rollback that needs it ends as upgrade.ErrNotRestored.
func (d *Driver) Restore(src string) error { return upgrade.ErrBackupUnsupported }

// Close closes the database handle when New opened it. A handle given to
// NewWithDB belongs to the caller and stays open.
func (d *Driver) Close() error {
	if !d.owned {
		return nil
	}
	return d.db.Close()
}

var (
	_ upgrade.SchemaDriver  = (*Driver)(nil)
	_ upgrade.Locker        = (*Driver)(nil)
	_ upgrade.VersionStorer = (*Driver)(nil)
)
