# Upgrade the binary and migrate its state

> Audience: Go adopters shipping a CLI who want it to check for new
> releases, replace itself, and move its on-disk state (databases,
> config, data dirs, JSON documents) forward across versions. Package
> reference: [`go/core/upgrade` README](../../../go/core/upgrade/README.md).

`hop.top/kit/go/core/upgrade` has two independent halves:

- `Checker`: poll a release feed, download, verify, replace the
  running binary.
- `Migrator`: run versioned `Up`/`Down` steps against named schemas
  with a backup first and automatic rollback on failure.

Use either alone.

## Part 1: self-update

### Prerequisites

- Releases published to GitHub (assets named with the OS and arch,
  e.g. `mytool_linux_amd64.tar.gz`), or a JSON endpoint of your own.
- A checksums asset next to the binaries (see "Verification").
- Your build stamps the running version (for example via `-ldflags`).

### 1. Build a Checker

```go
c := upgrade.New(
    upgrade.WithBinary("mytool", version),
    upgrade.WithGitHub("acme/mytool"),
)
```

Options (all in `config.go`): `WithGitHub(owner/repo)`,
`WithReleaseURL(url)` for a custom endpoint, `WithCacheTTL` (default
4h), `WithSnoozeDuration` (default 24h), `WithTimeout` (default 10s),
`WithStateDir` (default `$XDG_STATE_HOME/<binary>/upgrade`),
`WithChecksumURL`, `WithSkipVerify`, `WithTagPrefix("xray/v")` (this
tool's releases in a multi-component repo; other tags are ignored) and
`WithIncludePrereleases(true)`; either lists releases (3 pages of 100
at most) and takes the highest semver, where the default reads
`releases/latest`. With neither a repo nor a URL,
`Check` returns an error ("no release source configured").

A custom endpoint answers a GET with
`{"version":"1.3.0","url":"https://.../mytool_linux_amd64.tar.gz","notes":"..."}`.
For GitHub the asset is picked by matching the running OS and arch
(`amd64`/`x86_64`, `arm64`/`aarch64`) in the asset name; a leading `v`
on the tag is stripped. Release notes are cut at 1000 characters.

### 2. Expose an `upgrade` command

kit does not mount one for you. Wire the helper:

```go
cmd := &cobra.Command{
    Use:   "upgrade",
    Short: "Check for and install a newer release",
    RunE: func(cmd *cobra.Command, _ []string) error {
        return upgrade.RunCLI(cmd.Context(), c, upgrade.CLIOptions{
            AutoUpgrade: auto, // true: install without asking
            Quiet:       quiet, // true: silent when already current
            Out:         cmd.ErrOrStderr(),
        })
    },
}
```

`RunCLI` prints the update and notes, then asks `Upgrade now?
[y/N/snooze]` unless `AutoUpgrade` is set. The prompt reads os.Stdin;
for non-interactive callers (agents, CI) offer the `--auto` flag.
Answering `snooze` suppresses banners for the snooze duration.

Lower-level calls, if you build your own UI: `Check(ctx)` returns a
`*Result` (`Current`, `Latest`, `URL`, `Notes`, `UpdateAvail`, `Err`;
never nil, errors are in `Err`), `ShouldNotify(ctx)`, `Upgrade(ctx)`,
`Snooze()`, `WhatsNew(ctx)`.

### 3. Nudge at startup

```go
upgrade.NotifyIfAvailable(ctx, c, os.Stderr)
```

Prints one line (`[mytool] update available: 1.2.0 → 1.3.0`, then
the `mytool upgrade` hint) unless snoozed. Results are cached for
the TTL, so most runs make no network call; a failed check is silent.

### Verification and replacement

`Upgrade` downloads the asset beside the running binary, verifies its
SHA-256 against the checksums file, extracts the binary named
`BinaryName` from a `.tar.gz` (or takes a bare binary), then renames
it over the running executable.

- A checksums asset (name ending `checksums.txt`, `sha256sums` or
  `sha256sums.txt`) is found automatically on GitHub. With a custom
  endpoint, set `WithChecksumURL`. With neither, `Upgrade` refuses
  unless `WithSkipVerify(true)` (dev and testing only).
- `.zip` assets are not supported; publish `.tar.gz`.
- Verification is a checksum from the same release feed, not a
  signature check.

### Observe it on the bus

`upgrade.WithPublisher(pub)` emits `kit.core.upgrade.released`,
`.downloaded`, `.installed` and `.snoozed`; rename them with
`WithTopicPrefix("myapp.core.upgrade")` or per action with
`WithTopics`. Publishing is best effort and never fails an upgrade.
Without a publisher the Checker never touches the bus.

To tell a coding agent to keep the tool current, render the text with
[`go/core/upgrade/skill`](../../../go/core/upgrade/skill/README.md).

## Part 2: versioned state migrations

### 1. Pick a driver per store

A `SchemaDriver` names a store, reports its version and backs it up.
Shipped drivers (details in the
[driver README](../../../go/core/upgrade/driver/README.md)):

| Store | Package | Constructor |
|-------|---------|-------------|
| Config files | `driver/configfile` | `New(paths...)` |
| Data directory | `driver/fsdir` | `New(name, dirPath)` |
| SQLite file | `driver/sqlite` | `New(dbPath, opts...)`, `WithDB(db)` to use your handle |
| Shared TiDB/MySQL | `driver/tidb` | `New(dsn, table, opts...)` or `NewWithDB(db, table, opts...)`; locks with `GET_LOCK`, needs a pool of 2 or more (no backup: `ErrBackupUnsupported`) |
| Shared PostgreSQL | `driver/postgres` | `New(db, table, opts...)` over your `*sql.DB`; advisory lock (needs two or more connections, no transaction-mode pooler); no backup: `ErrBackupUnsupported` |

The driver's `Name()` is the key that ties migrations to it
(`Migration.Schema`). The file-backed drivers keep the version in a
file under the data dir; pass `WithTool("mytool")` so the path is
per-tool, and if you give the Migrator `WithDataDir`, give the driver
the same dir with its own `WithDataDir`.

### 2. Register migrations

```go
func init() {
    upgrade.RegisterMigration(upgrade.Migration{
        Version: "1.2.0",     // semver, pre-release allowed
        Schema:  "cache",     // the driver's Name()
        Up:      addIndexes,
        Down:    dropIndexes, // nil: no step-level rollback
        Verify:  checkIndexes, // optional; runs after Up
    })
}
```

`Register` panics when `Up` is nil. Pending steps for a schema are
those with `current < version <= target`, run in semver order.
`NewRegistry()` plus `WithRegistry` gives an isolated set (useful in
tests); `ResetRegistryForTest` clears the default.

### 3. Run them

```go
m := upgrade.NewMigrator("mytool", version,
    upgrade.WithVerify(checkFinalState),
    upgrade.WithBackupRetention(3), // default 5
)
m.AddDriver(fsdir.NewWithOptions("cache", cacheDir, fsdir.WithTool("mytool")))
if err := m.Run(ctx); err != nil {
    return err
}
```

Per schema, `Run` reads the stored version, backs the store up (under
`<data dir>/backups/<schema>/pre-<version>/`), applies each `Up` then
its `Verify`, records that migration's version, then runs the
`WithVerify` check. A crash between an `Up` and its record re-applies
that one migration on the next run, so keep migrations idempotent. On
failure it runs the applied `Down`s newest first, recording the
version before each, else restores the backup and records the version
the run started at. A failed verification satisfies
`errors.Is(err, upgrade.ErrVerify)`; a cancelled context is checked
between steps and wraps `context.Canceled`. `WithManualRollback()`
turns automatic rollback off. Old backups beyond the retention are
pruned after success.

A driver that cannot back its store up (a server database: `driver/tidb`)
returns `upgrade.ErrBackupUnsupported` from `Backup`: the run still goes
ahead, with no backup and no pruning for that schema. If a migration then
fails and a `Down` is missing or fails, nothing can restore the schema:
`Run` returns an error matching `upgrade.ErrNotRestored` (and
`ErrBackupUnsupported`) and publishes no `reverted` event, and the stored
version stays at the last step still applied. Give such a schema a `Down`
for every migration, or take a backup outside kit first.

`Status()` returns per-schema `Current`, `Target`, `Pending`;
`History()` the migrations applied this session.

#### Fresh and existing stores

A store that predates the migrator reports no version, so every
migration looks pending. The usual app creates its tables at the
current shape on first start, then runs the migrator. On a database it
just created, an additive migration (`ALTER TABLE ... ADD COLUMN`)
would apply on top of a table that already has the column and fail.
`WithBaseline` is what prevents that: when a driver reports `""`, the
Migrator calls it before it selects pending migrations, and records the
version it returns.

```go
func ensureTables(db *sql.DB) error { /* CREATE TABLE IF NOT EXISTS at the current shape */ }

m := upgrade.NewMigrator("mytool", version,
    upgrade.WithBaseline(func(ctx context.Context, schema string) (string, error) {
        // Inspect the live store: which shape is it at?
        has, err := columnExists(ctx, db, "versions", "live")
        if err != nil {
            return "", err // fails this schema's run, nothing applied
        }
        if has {
            return "1.2.0", nil // already at the shape of 1.2.0: stamp it
        }
        return "", nil // unversioned and older: every migration applies
    }),
)
if err := ensureTables(db); err != nil {
    return err
}
if err := m.Run(ctx); err != nil {
    return err
}
```

The function runs only for a store with no recorded version, once per
schema per run, never when a version exists. A non-empty answer is
recorded through the same path as a migration's version and publishes
`kit.core.migration.baselined`; migrations at or below it are not
applied, later ones run as usual. An empty answer changes nothing. An
error fails that schema (`baseline: ...`) before any backup or `Up`.

The introspection to copy is the one the document store already uses
(`engine/store/document.go`): its `live` column migration reads
`PRAGMA table_info(versions)` and adds the column only when it is
missing, because a fresh database got it from `CREATE TABLE`. Your
baseline function asks the same question of the live store and turns
the answer into a version. A fresh database and an upgraded one then
end at the same version with the same shape.

#### Several processes starting at once

A driver that also implements `upgrade.Locker`
(`Lock(ctx) (unlock func() error, err error)`) is locked before the
Migrator reads its version and unlocked when that schema's run ends:
the version read, the baseline, the backup, every `Up`, every version
record, a rollback and the backup prune all happen under the lock. A
second process blocks in `Lock` until the first unlocks (or its
context is done, which fails that schema with `lock: ...`), then reads
the version the first one recorded and finds nothing pending. An
`unlock` error is logged, not returned. `RollbackLatest` (the `migrate
rollback` command) takes the same lock around the restore and the version
record, so a manual rollback waits for a migrating process; a lock error
fails that schema's rollback (`rollback <schema>: lock: ...`). A driver
without `Locker` runs unlocked. In Rust the driver returns a `Locker` from
`SchemaDriver::as_locker`.

The SQLite driver is a `Locker` in both modes: an exclusive `flock` on
`.lock` beside its version file (`<data dir>/<name>/.lock`), so two
processes on one database exclude each other; the wait ends with the
context. Its backup is `VACUUM INTO`, which includes rows still in the
WAL. `New(dbPath)` opens its own connection for it and `Restore` swaps
the file back (close every handle on it first; `-wal`, `-shm` and
`-journal` are removed). `New(dbPath, sqlite.WithDB(db))` snapshots
through your handle, never closes it, and cannot restore under a live
pool: `Restore` returns `upgrade.ErrBackupUnsupported`, so give each
migration a `Down` and keep the snapshot for manual recovery.

### 4. Give users a `migrate` command

```go
root.AddCommand(upgrade.MigrateCommand(m, v)) // v is your *viper.Viper
```

Subcommands: `migrate status` (read), `migrate run` (write-local,
honors `--dry-run` with a plan), `migrate rollback` (destructive-local;
only with `WithManualRollback()`, restores the latest backup),
`migrate history` (read). The tree is reserved, so it is never exposed
over a served surface. `rollback` is gated like any destructive
command; see [guard-destructive-commands](guard-destructive-commands.md).
Migration progress events (`kit.core.migration.*`) go to
`WithEventPublisher`.

## Python: versioned state migrations

`hop_top_kit.upgrade` (`pip install hop-top-kit`) ships the same
`Migrator` for Python, bound to the same
[`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md)
vectors: per-schema drivers, a backup before the first `up`, a version
record after each step, rollback, baseline, locking and events. It is
synchronous: migrations and drivers are plain callables, and
cancellation is a `threading.Event`. Not ported: the `migrate` command
tree (build your own over `status()`, `run()`, `rollback_latest()` and
`history()`) and the `tidb` driver. Package reference:
[`hop_top_kit/upgrade`](../../../sdk/py/hop_top_kit/upgrade/README.md).

### Set it up at application start

Register migrations, add one driver per store, run before the app
touches the store. A migration is `Migration(version, schema, up, down=None,
verify=None)`; `schema` must equal the driver's `name`.

```python
import threading
from pathlib import Path

from hop_top_kit.upgrade import Migration, Migrator, register_migration
from hop_top_kit.upgrade.drivers import FsDirDriver

cache_dir = Path("cache")
cache_dir.mkdir(exist_ok=True)
data_dir = Path("state")  # version files and backups live here


def add_index() -> None:
    (cache_dir / "index.json").write_text("{}")


def drop_index() -> None:
    (cache_dir / "index.json").unlink()


register_migration(Migration("1.2.0", "cache", add_index, drop_index))

m = Migrator("mytool", "1.2.0", data_dir=data_dir)
m.add_driver(FsDirDriver("cache", cache_dir, data_dir=data_dir))
m.run(threading.Event())  # raises MigrateError on failure
print([(s.schema, s.current, s.target, s.pending) for s in m.status()])
print([(a.schema, a.version) for a in m.history()])
```

`run(cancel=None)` backs each store up under
`<data dir>/backups/<schema>/pre-<version>/` first, applies each `up`
then its `verify`, records that migration's version, then the
Migrator-wide `verify=` check. On failure it runs the applied `down`s
newest first, else restores the backup; `manual_rollback=True` turns
that off and leaves `rollback_latest()` to you. `backup_retention`
(default 5), `registry` (an isolated `Registry()` for tests), `bus` and
`topics` (events) are the other options. Keep migrations idempotent: a
crash between an `up` and its record re-applies that one.

### Pick a driver

| Store | Driver | Pick it when |
|-------|--------|--------------|
| Config files | `ConfigFileDriver(paths, name="config", data_dir=...)` | a migration rewrites YAML, TOML or JSON config in place |
| Data directory | `FsDirDriver(name, path, data_dir=...)` | caches or per-project state live in a directory |
| SQLite file | `SqliteDriver(db_path, name=..., data_dir=...)` or `connect=` | the store is one SQLite file |
| Shared PostgreSQL | `PostgresDriver(connect, table, name=...)` | several installs share one database and each migration must apply once |

The first three keep the version in a file under the data dir: give the
driver the Migrator's `data_dir` (or `tool=`). `PostgresDriver` keeps it
in a table row. Backup file layouts match the Go drivers, so either port
restores the other's backups.

```python
import threading
from pathlib import Path

from hop_top_kit.upgrade import Migrator
from hop_top_kit.upgrade.drivers import ConfigFileDriver, FsDirDriver, SqliteDriver

data_dir = Path("state")
Path("config.yaml").write_text("theme: dark\n")
Path("cache").mkdir(exist_ok=True)

m = Migrator("mytool", "1.2.0", data_dir=data_dir)
m.add_driver(ConfigFileDriver(["config.yaml"], data_dir=data_dir))  # name "config"
m.add_driver(FsDirDriver("cache", "cache", data_dir=data_dir))
m.add_driver(SqliteDriver("app.db", name="appdb", data_dir=data_dir))
m.run(threading.Event())  # no migrations registered: nothing to apply
print([s.schema for s in m.status()])
```

### A database that predates the migrator

A store with no recorded version looks fully unmigrated. The usual app
creates its tables at the current shape, then runs the migrator; an
additive migration (`ALTER TABLE ... ADD COLUMN`) would then fail on a
column that already exists. `baseline=` prevents that: when a driver
reports `""`, the Migrator calls `baseline(schema)` before it selects
pending migrations and records the version it returns. Migrations at or
below it are not applied. Return `""` to leave the store unversioned
(everything applies). It never runs once a version exists, and an
exception fails that schema before any backup or `up`.

```python
import sqlite3
import threading
from contextlib import closing
from pathlib import Path

from hop_top_kit.upgrade import Migration, Migrator, Registry
from hop_top_kit.upgrade.drivers import SqliteDriver

db_path = "app.db"
data_dir = Path("state")


def ensure_tables() -> None:  # the current shape: `note` is already there
    with closing(sqlite3.connect(db_path)) as c, c:
        c.execute(
            "CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, note TEXT)"
        )


def add_note() -> None:
    with closing(sqlite3.connect(db_path)) as c, c:
        c.execute("ALTER TABLE items ADD COLUMN note TEXT")


def baseline(schema: str) -> str:
    with closing(sqlite3.connect(db_path)) as c:
        cols = {row[1] for row in c.execute("PRAGMA table_info(items)")}
    return "1.1.0" if "note" in cols else ""


reg = Registry()
reg.register(Migration("1.1.0", "appdb", add_note))
m = Migrator("mytool", "1.1.0", data_dir=data_dir, registry=reg, baseline=baseline)
m.add_driver(SqliteDriver(db_path, name="appdb", data_dir=data_dir))
ensure_tables()
m.run(threading.Event())
print(m.status()[0].current, m.history())  # 1.1.0 [] : add_note never ran
```

A fresh database and an upgraded one end at the same version with the
same shape. The function asks the live store which shape it has and
turns the answer into a version.

### SQLAlchemy

Both SQL drivers take a connect callable that returns a PEP 249
connection: `engine.raw_connection` is that callable, no wrapper needed.

```python
import threading
from pathlib import Path

from sqlalchemy import create_engine

from hop_top_kit.upgrade import Migrator
from hop_top_kit.upgrade.drivers import SqliteDriver

engine = create_engine("sqlite:///app.db")
data_dir = Path("state")

m = Migrator("mytool", "1.1.0", data_dir=data_dir)
m.add_driver(SqliteDriver("app.db", connect=engine.raw_connection, name="appdb", data_dir=data_dir))
m.run(threading.Event())
```

What each driver does with the connections it gets:

- `SqliteDriver` with `connect=`: backup calls it, runs `VACUUM INTO`
  and never closes what it got. A pooled connection goes back to the
  pool when you drop the reference, not through an explicit `close()`.
  The connection must not be inside a transaction (including
  `autocommit=False`): backup then raises `UpgradeError` and changes
  nothing. `restore` raises `BackupUnsupported`: the file cannot be
  replaced under a live pool, so give each migration a `down` and keep
  the snapshot for manual recovery. Without `connect=`, `SqliteDriver(db_path)`
  opens its own connection and restores by swapping the file; no handle
  may be open on it then.
- `PostgresDriver`: closes only connections it got from `connect`, and
  only after unlocking them, because `close()` on a pooled connection
  returns it with the session lock still held. A failed unlock
  `invalidate()`s a pooled connection (or `close()`s a plain one). Any
  other connection of yours is never touched.

### PostgreSQL

```python
import os
import threading
from pathlib import Path

from sqlalchemy import create_engine

from hop_top_kit.upgrade import Migration, Migrator, Registry
from hop_top_kit.upgrade.drivers import PostgresDriver

# DATABASE_URL like postgresql+psycopg2://user:password@host:5432/app
engine = create_engine(os.environ["DATABASE_URL"], pool_size=3, max_overflow=0)

reg = Registry()
reg.register(Migration("1.1.0", "appdb", lambda: None))
m = Migrator("mytool", "1.1.0", data_dir=Path("state"), registry=reg)
m.add_driver(PostgresDriver(engine.raw_connection, "kit_versions", name="appdb"))
m.run(threading.Event())
print(m.status()[0].current)  # the version is a row in kit_versions
```

`connect` is any callable returning a new psycopg2 or psycopg 3
connection (`lambda: psycopg2.connect(dsn)` works). The table name must
match `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$` (`ValueError` before any I/O);
the driver creates it. The version lives in that table, not in a file
under `data_dir`.

Connection limits:

- `connect` must supply at least two connections at once: the lock holds
  one for the whole run, version reads and writes use another. Size a
  pool for that plus whatever your migrations check out themselves.
- No transaction-mode pooler (PgBouncer `pool_mode = transaction`) in
  front: a session advisory lock does not survive it.

A pool capped at one is refused at lock time instead of deadlocking.
`run` raises `MigrateError` with text
`migrate appdb: lock: postgres upgrade: lock needs a connect callable
that supplies at least two connections: ... (a pool capped at one would
deadlock)`; nothing is locked and nothing applied.

```python
import os
import threading
from pathlib import Path

from sqlalchemy import create_engine

from hop_top_kit.upgrade import Migrator
from hop_top_kit.upgrade.drivers import PostgresDriver

engine = create_engine(os.environ["DATABASE_URL"], pool_size=1, max_overflow=0)
m = Migrator("mytool", "1.1.0", data_dir=Path("state"))
m.add_driver(PostgresDriver(engine.raw_connection, "kit_versions", name="appdb", probe_timeout=1.0))
try:
    m.run(threading.Event())
except Exception as e:
    print(type(e).__name__, "at least two connections" in str(e))  # MigrateError True
```

### Not restored on Postgres

Kit does not back up a server database: `PostgresDriver.backup` raises
`BackupUnsupported`. The run still goes ahead, with no backup. If a
migration then fails and its `down` is missing or fails, nothing can
restore the schema: `run` raises a `MigrateError` that matches
`is_not_restored` (and `is_backup_unsupported`), publishes no
`reverted` event, and the stored version stays at the last step that
applied. Give such a schema a `down` for every migration, or take a
backup outside kit first.

```python
import os
import threading
from pathlib import Path

from sqlalchemy import create_engine

from hop_top_kit.upgrade import (
    MigrateError,
    Migration,
    Migrator,
    Registry,
    is_backup_unsupported,
    is_not_restored,
)
from hop_top_kit.upgrade.drivers import PostgresDriver


def broken() -> None:
    raise RuntimeError("boom")


reg = Registry()
reg.register(Migration("2.1.0", "appdb", lambda: None))  # applies, has no down
reg.register(Migration("2.2.0", "appdb", broken))  # fails, has no down
engine = create_engine(os.environ["DATABASE_URL"], pool_size=3, max_overflow=0)
driver = PostgresDriver(engine.raw_connection, "kit_versions", name="appdb")
m = Migrator("mytool", "2.2.0", data_dir=Path("state"), registry=reg)
m.add_driver(driver)
try:
    m.run(threading.Event())
except MigrateError as e:
    print(is_not_restored(e), is_backup_unsupported(e), driver.version())  # True True 2.1.0
```

Treat `is_not_restored` as "repair by hand": the store is at the last
recorded version, plus whatever the failed `up` did before it raised.
`SqliteDriver(..., connect=...)` ends the same way on a rollback that
needs a restore.

### Several processes starting at once

`PostgresDriver` and `SqliteDriver` are lockers: the Migrator locks a
schema before it reads the version and unlocks when that schema's run
ends, so the baseline, backup, every `up`, every version record and the
prune happen under it. A second process blocks, then reads the version
the first recorded and finds nothing pending. `ConfigFileDriver` and
`FsDirDriver` have no lock.

- SQLite: `flock(2)` on `<data dir>/<name>/.lock` (use the same
  `data_dir` in every process).
- Postgres: a session advisory lock keyed `kit-upgrade:<table>:<name>`.

A Go and a Python Migrator on the same SQLite data dir and name, or the
same Postgres table and name, use the same lock and exclude each other.
`status()` and `rollback_latest()` behave as in Go (`status()` takes no
lock). Bound the wait with the cancel event; a cancelled wait raises
`MigrateError` matching `is_canceled`, with nothing held:

```python
import threading
from pathlib import Path

from hop_top_kit.upgrade import Migrator, is_canceled
from hop_top_kit.upgrade.drivers import SqliteDriver

data_dir = Path("state")
m = Migrator("mytool", "1.1.0", data_dir=data_dir)
m.add_driver(SqliteDriver("app.db", name="appdb", data_dir=data_dir))

cancel = threading.Event()
timer = threading.Timer(30.0, cancel.set)  # give up waiting after 30s
timer.start()
try:
    m.run(cancel)
except Exception as e:
    if not is_canceled(e):
        raise
    print("another process is still migrating")
finally:
    timer.cancel()
```

For one JSON document in memory, use `migrate_document(data, [DocStep(1, 2, fn)])`;
see the package README.

## Migrating one JSON document in memory

For a settings or manifest document with an integer version field, no
I/O needed:

```go
steps := []upgrade.DocStep{{
    From: 1, To: 2, Note: "rename title",
    Apply: func(d map[string]any) (map[string]any, error) {
        d["name"] = d["title"]
        delete(d, "title")
        return d, nil
    },
}}
out, report, err := upgrade.MigrateDocument(doc, steps, upgrade.DocOptions{})
```

`{"version":1,"title":"x"}` becomes `{"name":"x","version":2}`; keys
come back sorted, numbers stay exact. `report.Applied` lists the steps.
Options: `VersionField` (dotted path, default `version`), `Latest`
(default: highest `To`), `MissingIsZero`. Errors satisfy `errors.Is`
with `ErrDocNewer` (document newer than known), `ErrDocVersion`,
`ErrDocGap`, `ErrDocStep`, `ErrDocInvalid`, or `ErrVerify`; on error
the input is untouched. `MigrateDocumentMap` is the same on a decoded
map and never modifies yours. Cross-language behavior is pinned by
[`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md).

## See also

- [`go/core/upgrade` README](../../../go/core/upgrade/README.md)
- [`hop_top_kit/upgrade` README](../../../sdk/py/hop_top_kit/upgrade/README.md)
  and its [drivers](../../../sdk/py/hop_top_kit/upgrade/drivers/README.md)
- [Upgrade migrations on SQL backends](../../contributors/architecture/upgrade-sql-backends.md):
  version authority, locking, baseline, backup limits, and when to use
  `storage/sqldb.Migrate` instead
- [guard-destructive-commands](guard-destructive-commands.md)
- [hook-cli-into-bus](hook-cli-into-bus.md)
