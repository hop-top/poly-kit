# drivers

## What it answers

Which store holds a schema's version and how it is backed up, restored and locked, for one `Migration.schema`. Each driver implements `SchemaDriver`; migration logic lives in the parent `hop_top_kit.upgrade` package.

## Use it when

| Driver | Constructor | Version lives in | Backup / restore |
|--------|-------------|------------------|------------------|
| `ConfigFileDriver` | `ConfigFileDriver(paths, *, name="config", tool="", data_dir=None)` | version file | copies each file under its flattened name; a missing file is skipped |
| `FsDirDriver` | `FsDirDriver(name, path, *, tool="", data_dir=None)` | version file | `<name>.tar.gz` of the directory; restore swaps a checked copy in |
| `SqliteDriver` | `SqliteDriver(db_path=None, *, connect=None, name="sqlite", tool="", data_dir=None)` | version file | `VACUUM INTO` snapshot; restore swaps the file, or is unsupported with `connect` |
| `PostgresDriver` | `PostgresDriver(connect, table, *, name="postgres", lock_poll=0.1, probe_timeout=10.0)` | table row (version storer: no version file) | both raise `BackupUnsupported`; back up outside kit |

- pick `ConfigFileDriver` for YAML/TOML/JSON config a migration rewrites in place
- pick `FsDirDriver` for a data directory (caches, per-project state)
- pick `SqliteDriver(db_path)` for a SQLite file, or `SqliteDriver(db_path, connect=engine.raw_connection)` when the app keeps it open through a pool
- pick `PostgresDriver(connect, table)` when several installs or replicas share one database and must apply each migration once; a Go and a Python migrator on the same `table` and `name` exclude each other

## Quick start

```python
from hop_top_kit.upgrade import Migrator
from hop_top_kit.upgrade.drivers import ConfigFileDriver, SqliteDriver

m = Migrator("mytool", "1.2.0", data_dir=data_dir)
m.add_driver(ConfigFileDriver(["/etc/mytool/config.yaml"], data_dir=data_dir))
m.add_driver(SqliteDriver("app.db", name="appdb", data_dir=data_dir))
m.run()
```

## Contract

- `name` must equal `Migration.schema`. Pass the Migrator's `data_dir` (or `tool`) so `version()` reads the version file the Migrator wrote; `set_version` does nothing.
- File names match the Go drivers, so a backup made by either port restores with the other. `configfile` names a file by its cleaned path with each separator replaced by `_`; `fsdir` writes a gzip tar with the directory as the first member `.`, relative names and empty directories kept.
- `FsDirDriver.restore` refuses absolute names, `..`, links that leave the directory, links whose target climbs with `..` after a name, members below or named like a link, and device or other special members (`UnsafeArchiveError`); it extracts beside the target and swaps only on success, so a failed restore leaves the directory as it was. A directory that did not exist at backup time leaves no archive and its restore raises `FileNotFoundError`.
- Symbolic links that restore accepts (relative, staying inside the directory) are archived and restored. `backup` raises `UnsafeArchiveError` naming the path for any link restore would refuse (absolute, leaving the directory, `..` after a name), and `OSError` for special files; it leaves no archive, so the Migrator fails the schema (`backup: ...`) before any migration runs. Python 3.11.0 to 3.11.3 have no `tarfile` extraction filters: there `backup` raises `OSError` on a symbolic link and `restore` raises `UnsafeArchiveError` on any link member. Use 3.11.4 or later for trees with links.
- `SqliteDriver` backup is `VACUUM INTO` with a bound parameter, via a `.tmp` file renamed into `<dest>/<db file name>`; WAL rows are included. A missing file means nothing to back up; without `db_path` the backup file is `<name>.db`.
- `SqliteDriver` path only: restore copies the backup beside the file, renames it over, then removes `-wal`, `-shm` and `-journal`; no open handle may exist on the file. With `connect`: backup runs on the connection it returns and never closes it; restore raises `BackupUnsupported`. A connection inside a transaction, or opened with `autocommit=False`, makes backup raise `UpgradeError` and changes nothing.
- `SqliteDriver` lock: `flock(2)` on `<data dir>/<name>/.lock`, created by the driver; the Go driver locks the same file, so both exclude each other. Windows uses `msvcrt.locking` (untested).
- `PostgresDriver`: `table` must match `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$` (`ValueError` before any I/O). `connect()` returns a new connection each call; the driver closes only those and never touches any other. SQL uses `%s` (psycopg2, psycopg 3). Table, upsert and lock key `kit-upgrade:<table>:<name>` are those of `go/core/upgrade/driver/postgres`.
- `PostgresDriver.lock(cancel)` polls `pg_try_advisory_lock` on one dedicated connection until acquired or cancelled (`Canceled`, nothing held), and unlocks with `pg_advisory_unlock` on that connection before releasing it: `close()` on a pooled connection would return it with the session lock still held. A failed unlock calls `invalidate()` when the connection has it (SQLAlchemy), else `close()`.
- `PostgresDriver` limits: `connect` must supply at least two connections at once (refused at `lock` otherwise, within `probe_timeout`); no transaction-mode pooler in front. Tests: `make test-py-upgrade-integration` (Docker, Postgres 16).

## Neighbours

- `go/core/upgrade/driver`: the Go drivers; `sdk/rs/src/upgrade`: the Rust drivers.
- [`hop_top_kit.upgrade`](../README.md): the `Migrator` and the driver protocol

## See also

- [`docs/adopters/guides/upgrade-and-migrate.md`](../../../../../docs/adopters/guides/upgrade-and-migrate.md)
