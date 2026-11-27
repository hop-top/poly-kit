# drivers

## What it answers

Where a schema's data and version live, and how it is backed up, restored and locked, for the `Migrator`. Each driver is a `SchemaDriver`; migration logic stays in `../`. Ported from Go `go/core/upgrade/driver`. Every driver is its own subpath import, so importing `@hop-top/kit/upgrade` never loads a database client, `tar` or `better-sqlite3`.

## Use it when

| Backend | Import | Constructor | Version lives in | Backup / restore |
|---------|--------|-------------|------------------|------------------|
| config files | `@hop-top/kit/upgrade/drivers/configfile` | `new ConfigFileDriver(paths, { name?, dataDir?, tool? })` | version file | copies each file to and from `dest` |
| a directory | `@hop-top/kit/upgrade/drivers/fsdir` | `new FsDirDriver(name, dirPath, { dataDir?, tool? })` | version file | `<name>.tar.gz`; restore extracts to a temp dir, then swaps (loads `tar`) |
| a SQLite file | `@hop-top/kit/upgrade/drivers/sqlite` | `new SqliteDriver(dbPath, { name?, db?, dataDir?, tool? })` | version file | `VACUUM INTO` (WAL-safe); restore swaps the file and drops `-wal`, `-shm`, `-journal` (loads `better-sqlite3`) |
| Postgres | `@hop-top/kit/upgrade/drivers/postgres` | `await openPostgresDriver({ db, connect, table, name? })` over your `pg` Pool | table row (`storesVersion`); advisory `lock` | both throw `BackupUnsupportedError`; back up outside kit |

- file-backed drivers read the Migrator's version file: give them the Migrator's `dataDir`
- `SqliteDriver` with `db`: backup runs on your `Database` and never closes it; `restore` throws `BackupUnsupportedError`, so a run with a missing `down` ends not restored
- pick Postgres when the schema lives in a shared database that several installs or a Go and a TypeScript tool migrate

## Quick start

```ts
import { Migrator } from '@hop-top/kit/upgrade';
import { SqliteDriver } from '@hop-top/kit/upgrade/drivers/sqlite';

const m = new Migrator('mytool', '1.2.0', { dataDir });
m.addDriver(new SqliteDriver('/data/app.db', { name: 'app', dataDir, db }));
await m.run();
```

## Contract

- `name()` must equal `Migration.schema`. Postgres defaults to `postgres`; its table matches `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`.
- Archive layout of `fsdir` is Go's: members relative to the directory, `.` for the directory itself. Either port restores the other's. A restore refuses absolute names, `..`, links, devices and other special members (`path traversal blocked`); a backup refuses a symbolic link.
- `SqliteDriver.lock` holds `BEGIN EXCLUSIVE` on `.lock.db` in `<data dir>/<schema>/`, retrying every 50 ms without blocking the event loop until acquired or `signal` aborts. Node has no `flock`: this does NOT exclude the Go, Python or Rust drivers, which flock `.lock`.
- `VACUUM INTO` cannot run in a transaction: a backup on a handle with one open fails with a clear error. Path-only restore needs no open handle on the file.
- Postgres: kit imports no Postgres client. `db` needs `query(text, params)`; `connect` returns a dedicated client (`pool.connect()`, or a connected `Client`) that holds the lock for the whole run. The pool needs at least two connections: `options.max === 1` is refused at lock time. No transaction-mode pooler in front.
- Tests: `*.integration.test.ts` need `better-sqlite3` or Postgres 16 in Docker and run under `make test-ts-upgrade-integration`; the rest run under `make test-ts-sdk`.

## Neighbours

- [`../README.md`](../README.md): `Migrator`, `SchemaDriver`; Go reference: [`go/core/upgrade/driver`](../../../../../go/core/upgrade/driver/README.md)
- [`go/core/upgrade/driver/postgres`](../../../../../go/core/upgrade/driver/postgres/README.md): the driver the Postgres one interoperates with.

## See also

- [`docs/adopters/reference/ts-api-reference.md`](../../../../../docs/adopters/reference/ts-api-reference.md)
