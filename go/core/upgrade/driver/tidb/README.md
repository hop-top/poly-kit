# tidb

## What it answers

Track a schema version in a TiDB or MySQL table instead of a local file.
Local stores use the sibling drivers listed in [driver/README.md](../README.md).

## Use it when

- several installs share one database and must agree on the applied version: `New(dsn, table)`
- the application already holds a `*sql.DB` and the driver should share it: `NewWithDB(db, table)`
- several processes may migrate at startup and only one must run: the driver is an `upgrade.Locker`
- the schema name must differ from the default `tidb`: `WithName(name)`

## Quick start

```go
_, err := tidb.New("root:pw@tcp(127.0.0.1:4000)/kit", "schema-versions")
fmt.Println(err)
// Output: invalid table name: "schema-versions"
```

With a valid table name `New` pings the server and creates the table;
`integration_test.go` runs that path against a MySQL container.

## Contract

- `New` opens the connection, pings it, and creates `<table>(schema_name VARCHAR(255) PRIMARY KEY, version VARCHAR(255) NOT NULL DEFAULT '')` if absent. The table name must match `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`.
- The driver is a `upgrade.VersionStorer`: the table row is the only version; the Migrator writes no version file for it.
- `Backup` and `Restore` return `upgrade.ErrBackupUnsupported`: kit does not back up a database. A run goes on without a backup; a failed migration whose `Down` is missing or fails is not restored (`upgrade.ErrNotRestored`) and the table keeps the last step still applied. Write a `Down` for each migration, or back up outside kit.
- `NewWithDB(db *sql.DB, table string, opts ...Option)` neither opens nor closes `db`; it ensures the table exists. `Close` closes only a handle `New` opened and never one the caller passed.
- `Lock` holds `GET_LOCK(name, 0)` on a dedicated `*sql.Conn` for the whole run, polling every 50ms on the client until it returns 1 or the context is done (no attempt waits on the server, so a cancelled `Lock` leaves no server-side wait that could later be granted the lock, and holds nothing). `unlock` runs `RELEASE_LOCK` on that connection ignoring cancellation; if that fails the physical connection is discarded, which ends the session and the lock. Calling `unlock` twice is safe.
- The lock name is `<table>:<schema>`, `<schema>` being `Name()`. The server accepts 64 characters: a longer name is cut to fit and ends in `#` plus 16 hex digits of the SHA-256 of the full name, so it stays deterministic and distinct.
- Limit: the handle must allow two open connections. `Lock` returns an error when `db.Stats().MaxOpenConnections` is 1 (0, unlimited, is fine); the held connection would leave none for `Version` and `SetVersion`.
- Limit: server lock names are server-wide, not per database. Two databases on one server that use the same table and schema names exclude each other; give them different table names. The lock covers kit's migrators only: it does not stop other clients from writing the schema.
- Call `Close` when done with a driver from `New`; it owns its `*sql.DB`.
- Imports `github.com/go-sql-driver/mysql`; the DSN follows that driver's format.

## Neighbours

- `go/storage/kv/tidb`: the key-value store that typically shares this DSN.
- `go/core/netpolicy`: `--offline` refuses the dial when the connection goes through a guarded dialer.

## See also

- [upgrade/README.md](../../README.md)
