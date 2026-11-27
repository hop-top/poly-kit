# Upgrade migrations on SQL backends

Status: accepted. Records how `core/upgrade` migrations behave when the
store is a SQL database that other processes share, so the Go
implementation, the vectors in
[`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md) and every
SDK port are written against one text. Go is canonical.

Defines seven rules: who owns the version, when it is recorded, how an
existing database is adopted, locking, backup and rollback honesty, how a
driver gets its connection, and how `core/upgrade` relates to
`storage/sqldb`. Behaviour the rules add is stated as the design; the
Context section lists what the code does today.

## Context

[`go/core/upgrade`](../../../go/core/upgrade/README.md) runs versioned
migrations per schema through a `SchemaDriver` (`Name`, `Version`,
`SetVersion`, `Backup`, `Restore`). Per schema, `Migrator.Run` reads the
stored version, selects the pending migrations, backs up, applies each
`Up` and its `Verify`, runs the `WithVerify` check, then records the last
applied version once. On failure it runs the applied `Down`s newest first,
and restores the backup when a `Down` is missing or fails.

That fits a file or a directory a single process owns. It leaves gaps for
a shared SQL database:

- the version is written once, after the last migration, so a process that
  dies mid-run leaves applied migrations unrecorded and the next run
  applies them again
- the Migrator writes a version file for every driver, then calls
  `SetVersion`; the TiDB driver keeps the version in its own table, so
  its version has two homes and a read-only data directory fails it
- an empty version makes every migration pending, so a database whose
  tables already exist at the current shape is migrated again
- two processes starting together both read the same version and both
  apply the migrations
- the TiDB driver's `Backup` and `Restore` return nil without doing
  anything, so a rollback that falls through to `Restore` is reported as a
  restore that never happened
- the SQLite and TiDB drivers open their own connection; an application
  that already holds a pool cannot hand it over

## Terms

| Term | Meaning |
|------|---------|
| schema | one store named by `SchemaDriver.Name()`; migrations target it through `Migration.Schema` |
| run | one `Migrator.Run`; schemas are processed one driver at a time, in `AddDriver` order |
| pre-run version | the version `Version()` reported for the schema when the run read it (after any baseline) |
| record | write a version: the version file (when the Migrator owns it), then `SetVersion` |
| handle | a connection or pool the caller already opened (`*sql.DB` in Go) |

## 1. Version authority

A driver that stores the version itself implements the marker capability
`VersionStorer`. For such a driver:

- its `Version()` and `SetVersion()` are the only authority for the schema
- the Migrator creates, writes and reads no version file for it, and
  creates no version directory under the data dir
- a read-only or missing data dir cannot fail a run for that driver, except
  through backups it still takes

Every other driver keeps today's behaviour: the Migrator writes
`<data dir>/<driver name>/version` and then calls `SetVersion`; the driver
reads the same file back (`ReadVersionFile`, `ReadVersionFileIn`) or ignores
`SetVersion`. If writing the file fails, `SetVersion` is not called.

`VersionStorer` is detected by interface assertion on the driver the
caller added. The TiDB and Postgres drivers implement it; SQLite
(file-based), `fsdir` and `configfile` do not.

Failure: an error from `Version()` fails the schema (`read version: ...`)
before anything runs. An error from `SetVersion` is a recording failure
(rule 2).

## 2. Version recorded after every applied migration

The version is recorded after each migration that applied, not once per run.

Sequence per migration, in order: `Up`, then its `Verify` when set, then
record the migration's version. The next migration starts only after the
record returned. The `WithVerify` final check runs after the last
migration's record.

Observable consequences:

- a run that stops at migration k leaves the stored version at the version
  of migration k-1 (the last applied and recorded), not at the pre-run
  version and not at the target
- a re-run after such a stop selects only the migrations after k-1
- a failed `Verify` (per migration) fails before that migration's record,
  so a migration that fails its own verify is never recorded
- the stored version describes the data after any rollback:
  - after every `Down` of the run succeeded, the Migrator records the
    pre-run version
  - after a `Restore` succeeded, the Migrator records the pre-run version
  - a final `WithVerify` failure rolls back every migration of the schema
    applied in the run, then records the pre-run version, even though each
    step was recorded on the way
  - an empty pre-run version is recorded as empty: the store reads back as
    unversioned
- with `WithManualRollback()` nothing is undone and nothing is recorded
  after a failure: the stored version stays at the last applied
  migration. `RollbackLatest` restores the latest backup, then records the
  version that backup was taken at; the backup carries that version
- a context canceled between steps stops before the next `Up`; recorded
  steps stay recorded, and auto rollback then runs as for any failure, so
  the stored version ends at the pre-run version (auto) or the last
  applied migration (manual)
- when rollback cannot complete (rule 5) nothing more is recorded; the
  stored version stays at whatever the last successful record wrote

Failure: when recording a migration's version fails, the step counts as
failed after its `Up` completed: the rollback includes that migration, the
error names the recording failure, and the stored version afterwards is the
pre-run version when the rollback completes. If the follow-up record of the
pre-run version fails too, the error reports both.

## 3. Baseline

`WithBaseline(fn func(ctx context.Context, schema string) (string, error))`
is a Migrator option. The Migrator calls it for a schema only when
`Version()` reports `""`, and only once per schema per run.

- the function returns the version the unversioned store should be treated
  as already at, typically the current shape of a database the application
  created itself, or `""` to leave the schema unversioned
- a non-empty result is recorded immediately (rule 2's record) and becomes
  the pre-run version; the pending selection (`from < version <= to`) then
  starts from it, so the migrations at or below it are not applied
- the Migrator publishes `kit.core.migration.baselined` after the record
  succeeded, with the schema and the recorded version as `to`
- a `""` result records nothing and emits nothing; every migration is
  pending as before
- a baseline above the target is recorded as is and nothing is pending
- the function is not called when the driver reports any non-empty version,
  and not when `Version()` fails

The baseline function is the only thing that prevents a fresh database,
created at the current shape by the application, from re-applying additive
migrations. It runs inside the lock (rule 4), so two processes that both
read `""` cannot both stamp and apply.

Failure: an error from `fn` fails the schema (`baseline: ...`); no backup,
no migration and no record happen for it.

## 4. Locking

Locking is an optional driver capability: `Locker`, with
`Lock(ctx) (unlock func() error, err error)`. A driver without it runs
unlocked, as today.

Per schema, in this order:

1. `Lock(ctx)`, when the driver implements `Locker`
2. `Version()`
3. baseline, when applicable (rule 3)
4. pending selection, backup, apply and record loop, final verify, rollback
   when needed, backup pruning
5. `unlock()`

The lock is acquired before the version is read and released after the run
for that schema has finished, on success, failure and rollback alike. The
Migrator calls `unlock` with a context that is not canceled, so a canceled
run still releases. `Lock` honours the run's context while it waits.
`Status()` takes no lock.

Locks are per schema, not per run: the Migrator holds one at a time and
releases it before the next driver starts. Two Migrators serialise only on
drivers that name the same lock.

For a driver whose lock is tied to a database session (a Postgres advisory
lock), the lock lives on one dedicated connection for the whole hold,
`unlock` is an explicit unlock statement on that connection, and returning
a connection to a pool never stands in for it. A handle that is a pool of
one connection is refused when the lock is taken, not deadlocked.

Failure: an error from `Lock` fails the schema (`lock: ...`) before the
version is read; nothing runs. An error from `unlock` after a successful
run is returned as the schema's error (`unlock: ...`) and undoes nothing;
after a failed run it is appended to the run's error.

## 5. Backup, restore and honest rollback

`ErrBackupUnsupported` is a sentinel. A driver whose store cannot be backed
up (or restored) by kit returns it from `Backup`, or from `Restore`; callers
test with `errors.Is`. There is no separate "can back up" capability and no
silent no-op.

Observable behaviour:

- `Backup` returns `ErrBackupUnsupported`: the run proceeds. No backup
  exists for the schema in this run, the Migrator leaves no backup
  directory behind for it, and nothing is pruned on its account
- any other `Backup` error fails the schema before any `Up` (`backup: ...`)
- when a failure triggers rollback, `Down`s run newest first as today.
  `Restore` is called only if a `Down` is missing or fails
- a rollback that needs `Restore` and has none to run (no backup was taken,
  or `Restore` returns `ErrBackupUnsupported`) fails: the returned error
  satisfies `errors.Is(err, ErrNotRestored)`, still carries the failure
  that started the rollback, and no `Reverted` event is published
- a rollback that completed through `Down`s alone is a revert (method
  `down`); one that completed through `Restore` is a revert (method
  `restore`). Nothing else is reported as a revert
- `RollbackLatest` on a schema without a backup fails the same way
  (`ErrNotRestored`); it never reports success without a restore

A run that never needed a rollback is unaffected by missing backups. This
is why an unsupported backup does not refuse the run up front: additive
migrations on a server database are applied, and only a failure that would
need a restore is reported as not restorable.

Drivers handed an existing handle (rule 6) to a SQLite file cannot replace
the file under a live pool: their `Restore` returns `ErrBackupUnsupported`.
Server-database drivers (TiDB, Postgres) return it from both `Backup` and
`Restore`.

Settled for the upgrade package: backup support is expressed per call by
`ErrBackupUnsupported`, not by a capability interface; a missing backup
never blocks a run and never masquerades as a restore.

## 6. Handles

SQL drivers accept a handle the caller already holds, instead of opening a
connection of their own. The driver does not close a handle it did not open.
Ports take the language's equivalent: a connection or a connect callable
(Python), a client and a connect closure (Rust). Kit runtime code imports
no Postgres driver in Go, Python, TypeScript or PHP; the caller supplies it.

`Migration.Up` keeps its `func(ctx context.Context) error` signature, and so
do `Down` and `Verify`. A migration that touches the database captures its
handle (or the application object holding it) in a closure when it
registers. The Migrator never passes a handle to a migration, and a driver
never exposes one.

Settled for the upgrade package: the migration mechanism is registered
functions over the caller's own handle, not SQL text owned by the Migrator.
The Migrator tracks versions, order, backup, locking and rollback; what a
step executes is the migration's business.

Failure: a driver given a handle it cannot use for the rule it implements
(a Postgres lock on a one-connection pool, see rule 4) reports that when
the capability is used, not when the driver is built.

## 7. Two mechanisms, kept separate

[`core/upgrade`](../../../go/core/upgrade/README.md) `Migrator` and
[`storage/sqldb`](../../../go/storage/sqldb/README.md) `Migrate` stay
separate packages with separate bookkeeping. Neither calls the other and
neither reads the other's tables.

| | `core/upgrade.Migrator` | `storage/sqldb.Migrate` |
|-|-------------------------|-------------------------|
| key | semver (`Migration.Version`) per schema | integer, `1..n`, one table per call |
| step | Go function (`Up`, optional `Down`, `Verify`) | one SQL string run in its own transaction |
| stores | several per tool: SQLite file, server database, files, directories | one SQLite database |
| around the step | backup, rollback, final verify, lock, baseline, events | nothing; applied versions are rows with a timestamp |

Use `core/upgrade` when the schema change ships with a tool release and
you want the version tied to it; when the state spans more than one store
or is not SQL; when the store is Postgres or TiDB; when you need rollback,
backup, a lock between concurrent processes, or the `kit.core.migration.*`
events.

Use `storage/sqldb.Migrate` when one SQLite database owns its own schema,
a numbered list of SQL statements is enough, and nothing needs rollback,
backup or events.

Do not run both against the same tables. A database may use `sqldb.Migrate`
for one schema and `core/upgrade` for another, under different table names.
`storage/sqlstore`'s WAL-safe `BackupBeforeMigrate` snapshot belongs to the
`sqldb` side; the upgrade drivers implement their own `Backup`.

## Vector surface

Rules 1 to 5 are the behaviour the `migrator.json` scenarios pin against a
recording driver, whose `calls` log lists every driver call in order
(`db backup pre-1.1.0`, `db up 1.1.0`, `db set_version 1.2.0`). The rules
add driver calls (`lock`, `unlock`, one `set_version` per applied
migration) and driver settings per scenario: whether the driver is a
`VersionStorer`, whether it is a `Locker` and whether `Lock` or `unlock`
fails, what `Backup` and `Restore` return, and whether a baseline function
is given. Expectations also cover the version files on disk (none for a
`VersionStorer`), the stored version, the error text and sentinel, and the
published events. Real drivers (rule 6) are tested in their own packages
and ports, not by the vectors.

## See also

- [`go/core/upgrade`](../../../go/core/upgrade/README.md)
- [`go/core/upgrade/driver`](../../../go/core/upgrade/driver/README.md)
- [`go/storage/sqldb`](../../../go/storage/sqldb/README.md)
- [`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md)
- [Upgrade the binary and migrate its state](../../adopters/guides/upgrade-and-migrate.md)
