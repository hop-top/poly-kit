# postgres

## What it answers

Track a schema version in a PostgreSQL table, and serialize concurrent
migrators with an advisory lock, over a `*sql.DB` the caller opens.
Local stores use the sibling drivers listed in [driver/README.md](../README.md).

## Use it when

- several installs or replicas share one database and must apply each migration once: `New(db, table)`
- the schema name must differ from the default `postgres`: `WithName(name)`

## Quick start

```go
db, _ := sql.Open("pgx", dsn) // any Postgres database/sql driver
d, err := postgres.New(db, "schema_versions", postgres.WithName("app"))
m := upgrade.NewMigrator("mytool", version)
m.AddDriver(d)
```

`New` creates the table, then `Run` locks, reads, migrates and unlocks.
`integration_test.go` runs it against a Postgres 16 container.

## Contract

- `New(db, table, opts...)` creates `<table>(schema_name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '')` if absent. The table name must match `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`. A nil `db` is an error.
- The caller owns `db`: the driver imports no Postgres driver, opens nothing, has no `Close` and never closes the handle.
- `upgrade.VersionStorer`: the row is the only version; the Migrator writes no version file. `SetVersion` upserts with `ON CONFLICT (schema_name) DO UPDATE`.
- `upgrade.Locker`: `Lock` runs `pg_advisory_lock(hashtextextended('kit-upgrade:<table>:<name>', 0))` on one dedicated connection and blocks until acquired or `ctx` is done. Unlock runs `pg_advisory_unlock` on that same connection, detached from `ctx`; if it fails the physical connection is discarded, which ends the session and the lock.
- Limit: the pool needs at least two connections (one holds the lock for the run, one does the work); a pool capped at one returns an error from `Lock`.
- Limit: no transaction-mode pooler (PgBouncer `transaction`) between the app and Postgres: a session lock does not survive it. Use a direct or session-mode connection.
- `Backup` and `Restore` return `upgrade.ErrBackupUnsupported`: a failed migration whose `Down` is missing or fails is not restored (`upgrade.ErrNotRestored`) and the row keeps the last step applied. With `WithManualRollback`, `RollbackLatest` finds no backup and returns an error; it restores nothing.

## Neighbours

- `go/core/upgrade`: `Migrator`, `Locker`, `VersionStorer`.
- `go/core/upgrade/driver/tidb`: the MySQL-compatible sibling.

## See also

- [upgrade/README.md](../../README.md)
