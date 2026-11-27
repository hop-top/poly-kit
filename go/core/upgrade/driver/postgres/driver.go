// Package postgres provides a SchemaDriver for PostgreSQL over a
// caller-provided *sql.DB. It imports no Postgres driver: the caller
// opens the handle with the driver of its choice.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"hop.top/kit/go/core/upgrade"
)

var validTable = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func validateTable(name string) error {
	if !validTable.MatchString(name) {
		return fmt.Errorf("invalid table name: %q", name)
	}
	return nil
}

// Driver implements upgrade.SchemaDriver, upgrade.VersionStorer and
// upgrade.Locker over a PostgreSQL table that tracks the schema version.
// It never opens or closes the *sql.DB it was given.
type Driver struct {
	db    *sql.DB
	name  string
	table string
}

// New ensures the version table exists on db and returns a driver for it.
// db belongs to the caller: the driver never closes it. The table name
// must match ^[a-zA-Z_][a-zA-Z0-9_]{0,63}$.
func New(db *sql.DB, table string, opts ...Option) (*Driver, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("postgres upgrade: nil *sql.DB")
	}
	d := &Driver{db: db, name: "postgres", table: table}
	for _, o := range opts {
		o(d)
	}
	if err := d.ensureTable(context.Background()); err != nil {
		return nil, fmt.Errorf("postgres upgrade: migrate: %w", err)
	}
	return d, nil
}

// ensureTable creates the version table under a transaction-scoped
// advisory lock: concurrent CREATE TABLE IF NOT EXISTS can still fail
// with a catalog unique violation when two sessions create the same
// table at once.
func (d *Driver) ensureTable(ctx context.Context) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, sqlXactLock, ddlKey(d.table)); err != nil {
		return err
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		schema_name TEXT PRIMARY KEY,
		version TEXT NOT NULL DEFAULT ''
	)`, d.table)
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Driver) Name() string { return d.name }

// Version returns the stored version; no row means "".
func (d *Driver) Version() (string, error) {
	q := fmt.Sprintf(`SELECT version FROM %s WHERE schema_name = $1`, d.table)
	var v string
	err := d.db.QueryRowContext(context.Background(), q, d.name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres upgrade: version: %w", err)
	}
	return v, nil
}

// SetVersion upserts the version row for this schema.
func (d *Driver) SetVersion(version string) error {
	q := fmt.Sprintf(`INSERT INTO %s (schema_name, version) VALUES ($1, $2)
		ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version`,
		d.table)
	if _, err := d.db.ExecContext(context.Background(), q, d.name, version); err != nil {
		return fmt.Errorf("postgres upgrade: set version: %w", err)
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

var (
	_ upgrade.SchemaDriver  = (*Driver)(nil)
	_ upgrade.VersionStorer = (*Driver)(nil)
	_ upgrade.Locker        = (*Driver)(nil)
)
