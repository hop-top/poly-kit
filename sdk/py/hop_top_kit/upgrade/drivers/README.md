# drivers

## What it answers

Which schema drivers ship with the Python `Migrator`, and how each treats backup, restore and locking. Wrong module for the driver protocol itself: that is `hop_top_kit.upgrade`.

## Use it when

- migrate a SQLite file: `SqliteDriver(db_path, name=..., tool=...)`
- migrate a SQLite file the app keeps open through a pool: `SqliteDriver(db_path, connect=engine.raw_connection)`

## Quick start

```python
import threading

from hop_top_kit.upgrade import Migrator
from hop_top_kit.upgrade.drivers import SqliteDriver

m = Migrator("mytool", "1.1.0")
m.add_driver(SqliteDriver("app.db", name="appdb", tool="mytool"))
m.run(threading.Event())
```

## Contract

- `SqliteDriver(db_path=None, *, connect=None, name="sqlite", tool="", data_dir=None)`; give `data_dir` when the `Migrator` has one. Without `db_path` the backup file is `<name>.db`.
- Backup is `VACUUM INTO` with a bound parameter, via a `.tmp` file renamed into `<dest>/<db file name>`; WAL rows are included. A missing file means nothing to back up.
- Path only: restore copies the backup beside the file, renames it over, then removes `-wal`, `-shm` and `-journal`; no open handle may exist on the file. The driver opens and closes its own connection to back up.
- With `connect`: backup runs on the connection it returns and never closes it; restore raises `BackupUnsupported`. A connection inside a transaction, or opened with `autocommit=False`, makes backup raise `UpgradeError` and changes nothing.
- Lock: `flock(2)` on `<data dir>/<name>/.lock`, created by the driver; the Go driver locks the same file, so both exclude each other. Windows uses `msvcrt.locking` (untested). `set_version` is a no-op: the `Migrator` writes the version file.
- Parity: `go/core/upgrade/driver/sqlite`.

## Neighbours

- Go reference: `go/core/upgrade/driver/sqlite`
- [`hop_top_kit.upgrade`](../README.md): the `Migrator` and the driver protocol

## See also

- [`docs/adopters/guides/upgrade-and-migrate.md`](../../../../../docs/adopters/guides/upgrade-and-migrate.md)
