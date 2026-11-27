# upgrade

## What it answers

How a Python tool checks for a newer release, orders versions the way kit does, registers versioned migrations, migrates one JSON document by its own version field and runs migrations across schema drivers with a `Migrator`. Wrong module for self-update (download and replace): that half of `go/core/upgrade` is Go-only. Ships the driver protocol and the drivers in [`drivers/`](drivers/README.md): `ConfigFileDriver`, `FsDirDriver` and `SqliteDriver`.

## Use it when

- startup notice of a newer release: `create_checker(CheckerOptions(...)).notify_if_available()`
- strict check for `upgrade --check`: `check()` raises `UpgradeCheckError`
- order or select migration versions: `compare_versions(a, b)`, `Registry.pending(schema, from_, to)`
- upgrade a stored JSON document: `migrate_document(bytes, steps, DocOptions(...))`
- migrate stores: `Migrator(tool, version, bus=...)`, `add_driver(d)`, `run(cancel)`, `status()`, `rollback_latest()`
- migrate a SQLite file: `SqliteDriver(path)` or `SqliteDriver(path, connect=engine.raw_connection)` from `hop_top_kit.upgrade.drivers`

## Quick start

```python
from hop_top_kit.upgrade import DocStep, migrate_document

steps = [DocStep(1, 2, lambda d: {**d, "title": d.pop("name")}, note="rename")]
out, report = migrate_document(b'{"version": 1, "name": "x"}', steps)
```

## SQLite driver

```python
import sqlite3
from contextlib import closing

from hop_top_kit.upgrade.drivers import SqliteDriver


# Pre-versioning install: baseline stamps it, the additive migration runs once.
def baseline(schema):  # a fresh database already has the column: nothing runs
    with closing(sqlite3.connect(db_path)) as c:
        cols = {r[1] for r in c.execute("PRAGMA table_info(items)")}
    return "" if not cols else "1.1.0" if "note" in cols else "1.0.0"


m = Migrator("mytool", "1.1.0", baseline=baseline)
m.add_driver(SqliteDriver(db_path, name="appdb", tool="mytool"))
m.run(threading.Event())
```

- Backup is `VACUUM INTO` (WAL rows included, never a file copy), to `<dest>/<db file name>`; a missing file means nothing to back up.
- Path only: backup uses its own connection; restore swaps the file and drops `-wal`, `-shm`, `-journal`; no open handle may exist on the file then.
- With `connect` (returns a PEP 249 connection): backup runs on it and never closes it; restore raises `BackupUnsupported`. A connection inside a transaction (also `autocommit=False`) makes backup raise `UpgradeError`: end it first.
- Lock: `flock(2)` on `<data dir>/<name>/.lock`, the Go driver's file, so Go and Python Migrators exclude each other (`msvcrt.locking` on Windows, untested).

## Contract

- Version order is kit's lenient one (`v` and build metadata dropped, missing parts 0, pre-release identifier rules), not `packaging`.
- `Registry.register` raises `ValueError` when `up` is missing; `register_migration` feeds `default_registry()`.
- Document errors subclass `DocumentError`: newer, version, gap, step, invalid, and `DocVerifyError` (also a `VerifyError`). The input is never modified; a current document comes back byte-identical.
- A driver has `name`, `version()`, `set_version(v)`, `backup(dest)`, `restore(src)`; optional `Locker.lock(cancel) -> unlock` and `VersionStorer.stores_version()` are found with `isinstance`. `backup`/`restore` raise `BackupUnsupported` when they cannot.
- `run(cancel)` takes a `threading.Event`; set, it stops before the next `up` and wakes a waiting lock. Failures are `MigrateError`; match with `is_not_restored`, `is_backup_unsupported`, `is_verify`, `is_canceled`.
- `drivers.ConfigFileDriver(paths, name="config", data_dir=...)` copies files under flattened names, `drivers.FsDirDriver(name, path, data_dir=...)` writes `<name>.tar.gz`; both match Go's backup layout and restore archives with a traversal guard, swapping a temp dir in only on success.
- Events go to a `hop_top_kit.bus.Bus`; warnings to the `hop_top_kit.upgrade` logger.
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md), all three files, replayed by `tests/test_upgrade_conformance.py`.

## Neighbours

- Go reference: `go/core/upgrade`; Rust port: [`sdk/rs/src/upgrade`](../../../rs/src/upgrade/README.md)

## See also

- [`docs/adopters/guides/upgrade-and-migrate.md`](../../../../docs/adopters/guides/upgrade-and-migrate.md)
