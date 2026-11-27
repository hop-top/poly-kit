# upgrade

## What it answers

How a TypeScript tool checks for a newer release, orders migration versions leniently, registers versioned schema migrations and migrates one JSON document by its own version field, and runs migrations across schema drivers with backup, lock, baseline and rollback (`Migrator`). Same semantics as Go `core/upgrade`. One driver ships: Postgres (`@hop-top/kit/upgrade/drivers/postgres`); write others against `SchemaDriver`. Wrong module for self-update (download, replace): Go only.

## Use it when

- announce a newer release: `createChecker({ name, currentVersion, owner, repo })`, then `notifyIfAvailable(process.stderr)`
- order or select migrations: `compareVersions('v1.9.0', '1.10.0')`, `new Registry().pending(schema, from, to)`
- migrate a JSON document: `migrateDocument(text, steps, { latest: 3 })`
- migrate stores: `new Migrator(tool, version, opts)`, `addDriver(d)`, `await run(signal?)`, `status()`, `rollbackLatest()`
- share one Postgres database across installs: `await openPostgresDriver({ db: pool, connect: () => pool.connect(), table, name })`

## Quick start

```ts
import { migrateDocument, type DocStep } from '@hop-top/kit/upgrade';

const steps: DocStep[] = [
  { from: 1, to: 2, apply: (d) => ({ ...d, title: d.name }), note: 'add title' },
];
const { doc, report } = migrateDocument('{"version":1,"name":"x"}', steps);
```

## Contract

- Versions: a leading `v` and `+build` are dropped; missing or non-numeric parts are 0; a release sorts above its pre-releases. Not the strict `semver` package.
- Documents: the chain follows versions, not step order; newer, versionless or gapped documents fail with a `DocError` whose `kind` says why; the input is never modified; a current document comes back byte for byte.
- Migrator: per schema, lock, read version, baseline, backup, each `up` then `verify` then the version record, rollback, prune, unlock. `run` and `status` return Promises; `signal` aborts between steps (`context canceled`).
- Drivers: `SchemaDriver` methods may be sync or async. Optional `lock(signal)` returns an unlock; `storesVersion: true` means the driver owns its version, else the Migrator writes `<data dir>/<schema>/version`. Backups: `<data dir>/backups/<schema>/pre-<v>/` plus `pre-<v>.version`.
- Errors: a driver throws `BackupUnsupportedError` when it cannot back up or restore; `isBackupUnsupported`, `isNotRestored`, `isVerifyError`, `isCanceled` walk the run error's `cause` chain. Events go to any `{ publish(event) }` (a kit `Bus`).
- Postgres: the table is `<table>(schema_name, version)`; the lock is a session advisory lock on `kit-upgrade:<table>:<name>` (same key as Go, so Go and TypeScript Migrators exclude each other) taken on a dedicated `connect()` client by polling `pg_try_advisory_lock` about every 50ms, so an aborted wait rejects with `abortError(signal)` and holds nothing. Unlock runs `pg_advisory_unlock` on that client, then releases it; if that fails the client is destroyed (`release(true)`). `db.options.max === 1` is refused at lock time: the lock holds one connection. No transaction-mode pooler. No backup: a failed run without `down` is `NotRestoredError`. `pg` is the caller's, kit imports none.
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md) `versions.json`, `document.json` and `migrator.json`, replayed by [`conformance.test.ts`](conformance.test.ts).

## Neighbours

- Go reference: [`go/core/upgrade`](../../../../go/core/upgrade/README.md); Rust port: [`sdk/rs/src/upgrade`](../../../rs/src/upgrade/README.md)

## See also

- [`docs/adopters/reference/ts-api-reference.md`](../../../../docs/adopters/reference/ts-api-reference.md)
