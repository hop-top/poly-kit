/**
 * A {@link SchemaDriver} for PostgreSQL over a caller-provided
 * node-postgres style handle, ported from Go
 * `core/upgrade/driver/postgres`. Kit has no runtime Postgres dependency:
 * the caller opens the `pg` Pool (or any object with the same `query`
 * shape) and passes it in, plus a function that returns a dedicated client
 * for the lock.
 *
 * SQL, the table-name rule and the lock key text are the Go driver's,
 * character for character, so a Go and a TypeScript Migrator on one table
 * and schema name exclude each other.
 *
 * Import from `@hop-top/kit/upgrade/drivers/postgres`. The class is a
 * structural `SchemaDriver` (checked in `postgres.test.ts`); it imports no
 * type from `../driver` so the published declarations stay one chunk per
 * entry.
 */

import { BackupUnsupportedError, abortError, errorText } from '../errors';

/** What a query resolves with: the rows, as objects keyed by column. */
export interface PgResult {
  rows: Array<Record<string, any>>;
}

/** The node-postgres `query(text, params)` shape (a `Pool` or `Client`). */
export interface PgQueryable {
  query(text: string, params?: any[]): Promise<PgResult>;
  /** A node-postgres `Pool` carries its options; `max: 1` is refused at lock time. */
  readonly options?: { max?: number };
}

/**
 * A dedicated connection for the lock: a client from `pool.connect()` (has
 * `release`) or a standalone connected `Client` (has `end`). The driver
 * ends the physical connection through whichever exists.
 */
export interface PgLockClient {
  query(text: string, params?: any[]): Promise<PgResult>;
  /** Pooled client: `release()` returns it, `release(true)` destroys it. */
  release?(destroy?: boolean | Error): void;
  /** Standalone client: closes the connection. */
  end?(): void | Promise<void>;
}

export interface PostgresDriverOptions {
  /** Runs the version queries. Never closed by the driver. */
  db: PgQueryable;
  /**
   * Returns a dedicated connected client, e.g. `() => pool.connect()`.
   * It carries the lock for the whole run, so it must not be `db` itself
   * and a pool needs at least two connections.
   */
  connect: () => Promise<PgLockClient>;
  /** Version table, `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`, interpolated unquoted. */
  table: string;
  /** Schema name, matched against `Migration.schema`. Default `postgres`. */
  name?: string;
}

const TABLE_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;

/** SQL shared verbatim by every port (the server hashes the key text). */
export const SQL_LOCK = 'SELECT pg_advisory_lock(hashtextextended($1, 0))';
export const SQL_UNLOCK = 'SELECT pg_advisory_unlock(hashtextextended($1, 0))';
export const SQL_XACT_LOCK = 'SELECT pg_advisory_xact_lock(hashtextextended($1, 0))';
/**
 * The non-blocking form of {@link SQL_LOCK}: same key, same session-level
 * lock, so it excludes (and is excluded by) a blocking `pg_advisory_lock`
 * on the key. Polled so an aborted wait holds nothing.
 */
export const SQL_TRY_LOCK = 'SELECT pg_try_advisory_lock(hashtextextended($1, 0))';

/** Text hashed into the advisory lock key for a run. */
export const lockKey = (table: string, name: string): string => `kit-upgrade:${table}:${name}`;
/** Text hashed into the key that serialises table creation. */
export const ddlKey = (table: string): string => `kit-upgrade:${table}:ddl`;

/** The DDL for the version table. */
export const createTableSql = (table: string): string =>
  `CREATE TABLE IF NOT EXISTS ${table} (schema_name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '')`;
export const versionSql = (table: string): string => `SELECT version FROM ${table} WHERE schema_name = $1`;
export const upsertSql = (table: string): string =>
  `INSERT INTO ${table} (schema_name, version) VALUES ($1, $2) ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version`;

/** How often a blocked lock polls the server. */
const POLL_MS = 50;

/** Throws `invalid table name: "<name>"` unless `name` is a legal table name. */
export function validateTable(name: string): void {
  if (typeof name !== 'string' || !TABLE_RE.test(name)) {
    throw new Error(`invalid table name: ${JSON.stringify(name)}`);
  }
}

const firstValue = (r: PgResult): unknown => {
  const row = r.rows[0];
  return row === undefined ? undefined : Object.values(row)[0];
};

/** Ends the physical connection: the session, and every lock it holds, goes. */
async function discard(c: PgLockClient): Promise<void> {
  try {
    if (typeof c.release === 'function') c.release(true);
    else if (typeof c.end === 'function') await c.end();
  } catch {
    // Nothing more to do for a connection that cannot even close.
  }
}

/** Hands a healthy client back (pool) or closes it (standalone). */
async function finish(c: PgLockClient): Promise<void> {
  if (typeof c.release === 'function') c.release();
  else if (typeof c.end === 'function') await c.end();
}

/** Resolves after `ms`, or early when `signal` aborts. */
function pause(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const done = (): void => {
      clearTimeout(t);
      signal?.removeEventListener('abort', done);
      resolve();
    };
    const t = setTimeout(done, ms);
    signal?.addEventListener('abort', done, { once: true });
  });
}

/**
 * Tracks a schema version in a PostgreSQL table and serialises concurrent
 * migrators with a session-level advisory lock. Create it with
 * {@link openPostgresDriver}.
 */
export class PostgresDriver {
  readonly storesVersion = true;

  /** @internal use {@link openPostgresDriver}. */
  constructor(private readonly o: Required<PostgresDriverOptions>) {}

  name(): string {
    return this.o.name;
  }

  /** The stored version; no row means `""`. */
  async version(): Promise<string> {
    try {
      const r = await this.o.db.query(versionSql(this.o.table), [this.o.name]);
      const v = r.rows[0]?.['version'];
      return typeof v === 'string' ? v : '';
    } catch (e) {
      throw new Error(`postgres upgrade: version: ${errorText(e)}`, { cause: e });
    }
  }

  /** Upserts the version row for this schema. */
  async setVersion(version: string): Promise<void> {
    try {
      await this.o.db.query(upsertSql(this.o.table), [this.o.name, version]);
    } catch (e) {
      throw new Error(`postgres upgrade: set version: ${errorText(e)}`, { cause: e });
    }
  }

  /** Kit does not back up a server database: the run goes on without one. */
  async backup(_dest: string): Promise<void> {
    throw new BackupUnsupportedError('postgres upgrade: backup unsupported');
  }

  /** Nothing is restored: a rollback that needs it ends "not restored". */
  async restore(_src: string): Promise<void> {
    throw new BackupUnsupportedError('postgres upgrade: restore unsupported');
  }

  /**
   * Takes the session-level advisory lock on one dedicated connection and
   * holds that connection until the returned unlock runs. Waits by polling
   * `pg_try_advisory_lock` (about every 50 ms) until acquired or `signal`
   * aborts; an aborted wait rejects with `abortError(signal)` and the
   * connection is destroyed, so nothing is held. Unlock runs
   * `pg_advisory_unlock` on that same connection regardless of `signal`;
   * when it fails or answers `false` the connection is destroyed, which
   * ends the session and the lock.
   *
   * A pool with `options.max === 1` is refused (the lock holds its only
   * connection and the run's queries would wait for ever).
   */
  async lock(signal?: AbortSignal): Promise<() => Promise<void>> {
    if (this.o.db.options?.max === 1) {
      throw new Error(
        'postgres upgrade: lock needs a pool of at least two connections: ' +
          'the lock holds one for the whole run (a pool with max 1 would deadlock)',
      );
    }
    if (signal?.aborted) throw abortError(signal);
    let client: PgLockClient;
    try {
      client = await this.o.connect();
    } catch (e) {
      throw new Error(`postgres upgrade: lock: ${errorText(e)}`, { cause: e });
    }
    const key = lockKey(this.o.table, this.o.name);
    try {
      for (;;) {
        if (signal?.aborted) throw abortError(signal);
        const r = await client.query(SQL_TRY_LOCK, [key]);
        if (firstValue(r) === true) break;
        await pause(POLL_MS, signal);
      }
      // The acquiring statement may have been in flight when the abort hit.
      if (signal?.aborted) throw abortError(signal);
    } catch (e) {
      await discard(client);
      if (signal?.aborted) throw abortError(signal);
      throw new Error(`postgres upgrade: lock: ${errorText(e)}`, { cause: e });
    }
    return this.unlocker(client, key);
  }

  private unlocker(client: PgLockClient, key: string): () => Promise<void> {
    let result: Promise<void> | undefined;
    return () => (result ??= this.release(client, key));
  }

  private async release(client: PgLockClient, key: string): Promise<void> {
    let err: unknown;
    try {
      const r = await client.query(SQL_UNLOCK, [key]);
      if (firstValue(r) !== true) err = new Error('lock was not held by this session');
    } catch (e) {
      err = e;
    }
    if (err !== undefined) {
      await discard(client);
      throw new Error(`postgres upgrade: unlock: ${errorText(err)}`, { cause: err });
    }
    // The statement released the session lock; now the connection can go back.
    await finish(client);
  }
}

/**
 * Ensures the version table exists, then returns a driver for it. The
 * table is created in a transaction on a dedicated client that first takes
 * `pg_advisory_xact_lock`: two sessions running `CREATE TABLE IF NOT
 * EXISTS` at once can still fail with a catalog unique violation. The
 * driver never closes `db`.
 */
export async function openPostgresDriver(opts: PostgresDriverOptions): Promise<PostgresDriver> {
  validateTable(opts.table);
  if (!opts.db || typeof opts.db.query !== 'function') {
    throw new Error('postgres upgrade: db must have query(text, params)');
  }
  if (typeof opts.connect !== 'function') {
    throw new Error('postgres upgrade: connect must return a dedicated client');
  }
  const o: Required<PostgresDriverOptions> = { ...opts, name: opts.name ?? 'postgres' };
  try {
    await ensureTable(o);
  } catch (e) {
    throw new Error(`postgres upgrade: migrate: ${errorText(e)}`, { cause: e });
  }
  return new PostgresDriver(o);
}

async function ensureTable(o: Required<PostgresDriverOptions>): Promise<void> {
  const client = await o.connect();
  try {
    await client.query('BEGIN');
    await client.query(SQL_XACT_LOCK, [ddlKey(o.table)]);
    await client.query(createTableSql(o.table));
    await client.query('COMMIT');
  } catch (e) {
    try {
      await client.query('ROLLBACK');
    } catch {
      await discard(client);
      throw e;
    }
    await finish(client);
    throw e;
  }
  await finish(client);
}
