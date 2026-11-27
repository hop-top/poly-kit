# drivers

## What it answers

Which store holds the schema version for one `Migration.schema`, as a `SchemaDriver` the `Migrator` runs. One entry per backend, so importing `@hop-top/kit/upgrade` never loads a database client.

## Use it when

| Backend | Import | Constructor | Version lives in | Backup / restore |
|---------|--------|-------------|------------------|------------------|
| Postgres | `@hop-top/kit/upgrade/drivers/postgres` | `await openPostgresDriver({ db, connect, table, name? })` over your `pg` Pool | table row (`storesVersion`); advisory `lock` | both throw `BackupUnsupportedError`; back up outside kit |

- pick Postgres when the schema lives in a shared database that several installs or a Go and a TypeScript tool migrate

## Quick start

```ts
const pool = new Pool({ connectionString, max: 4 });
const d = await openPostgresDriver({ db: pool, connect: () => pool.connect(), table: 'schema_versions', name: 'app' });
const m = new Migrator('mytool', '1.2.0');
m.addDriver(d);
```

## Contract

- `name()` must equal `Migration.schema`. Postgres defaults to `postgres`; the table matches `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`.
- Kit imports no Postgres client. `db` needs `query(text, params)`; `connect` returns a dedicated client (`pool.connect()`, or a connected `Client`) that holds the lock for the whole run.
- The pool needs at least two connections: `options.max === 1` is refused at lock time. No transaction-mode pooler in front.
- Tests: `postgres.test.ts` needs no database; `postgres.integration.test.ts` starts Postgres 16 in Docker (`make test-ts-upgrade-integration`).

## Neighbours

- [`../README.md`](../README.md): `Migrator`, `SchemaDriver`.
- [`go/core/upgrade/driver/postgres`](../../../../../go/core/upgrade/driver/postgres/README.md): the reference this driver interoperates with.

## See also

- [`docs/adopters/reference/ts-api-reference.md`](../../../../../docs/adopters/reference/ts-api-reference.md)
