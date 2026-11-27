// Unit tests for the Postgres driver that need no database: a fake client
// records the SQL and stands in for the server.

import { describe, expect, it } from 'vitest';

import type { SchemaDriver } from '../driver';
import { isBackupUnsupported, isCanceled } from '../errors';
import {
  PostgresDriver,
  SQL_LOCK,
  SQL_TRY_LOCK,
  SQL_UNLOCK,
  SQL_XACT_LOCK,
  createTableSql,
  ddlKey,
  lockKey,
  openPostgresDriver,
  upsertSql,
  validateTable,
  versionSql,
  type PgLockClient,
  type PgQueryable,
  type PgResult,
} from './postgres';

interface Call {
  text: string;
  params?: unknown[];
}

class FakeClient implements PgLockClient {
  calls: Call[] = [];
  released: Array<boolean | Error | undefined> = [];
  /** Answers per statement text; default is no rows. */
  answers = new Map<string, (params?: unknown[]) => PgResult | Promise<PgResult>>();
  async query(text: string, params?: unknown[]): Promise<PgResult> {
    this.calls.push({ text, params });
    const a = this.answers.get(text);
    return a ? a(params) : { rows: [] };
  }
  release(destroy?: boolean | Error): void {
    this.released.push(destroy);
  }
}

const rows = (v: unknown): PgResult => ({ rows: [{ col: v }] });

function driverOver(db: PgQueryable, client: PgLockClient, name?: string): Promise<PostgresDriver> {
  return openPostgresDriver({ db, connect: async () => client, table: 'schema_versions', name: name ?? 'app' });
}

// Compile-time: the driver is a SchemaDriver the Migrator accepts.
export const _asSchemaDriver = (d: PostgresDriver): SchemaDriver => d;

describe('validateTable', () => {
  const ok = ['schema_versions', '_t', 'T1', 'a'.repeat(64)];
  const bad = ['', '1abc', 'a-b', 'a b', 'a.b', 'a;drop table x', 'a'.repeat(65), '"quoted"', 'naïve'];
  it.each(ok)('accepts %s', (n) => expect(() => validateTable(n)).not.toThrow());
  it.each(bad)('rejects %j with the Go message', (n) => {
    expect(() => validateTable(n)).toThrow(`invalid table name: ${JSON.stringify(n)}`);
  });
});

describe('SQL text and keys (pinned to the Go driver)', () => {
  it('has the shared statements', () => {
    expect(SQL_LOCK).toBe('SELECT pg_advisory_lock(hashtextextended($1, 0))');
    expect(SQL_UNLOCK).toBe('SELECT pg_advisory_unlock(hashtextextended($1, 0))');
    expect(SQL_XACT_LOCK).toBe('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))');
    expect(SQL_TRY_LOCK).toBe('SELECT pg_try_advisory_lock(hashtextextended($1, 0))');
    expect(createTableSql('t')).toBe(
      "CREATE TABLE IF NOT EXISTS t (schema_name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '')",
    );
    expect(versionSql('t')).toBe('SELECT version FROM t WHERE schema_name = $1');
    expect(upsertSql('t')).toBe(
      'INSERT INTO t (schema_name, version) VALUES ($1, $2) ' +
        'ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version',
    );
  });
  it('builds the lock key text', () => {
    expect(lockKey('schema_versions', 'app')).toBe('kit-upgrade:schema_versions:app');
    expect(ddlKey('schema_versions')).toBe('kit-upgrade:schema_versions:ddl');
  });
});

describe('openPostgresDriver', () => {
  it('validates the table before any I/O', async () => {
    let connected = false;
    const db: PgQueryable = { query: async () => ({ rows: [] }) };
    await expect(
      openPostgresDriver({ db, connect: async () => ((connected = true), new FakeClient()), table: 'a-b' }),
    ).rejects.toThrow('invalid table name: "a-b"');
    expect(connected).toBe(false);
  });

  it('creates the table in a transaction under the DDL lock, then releases the client', async () => {
    const c = new FakeClient();
    const d = await driverOver({ query: async () => ({ rows: [] }) }, c);
    expect(c.calls.map((x) => x.text)).toEqual([
      'BEGIN',
      SQL_XACT_LOCK,
      createTableSql('schema_versions'),
      'COMMIT',
    ]);
    expect(c.calls[1].params).toEqual(['kit-upgrade:schema_versions:ddl']);
    expect(c.released).toEqual([undefined]);
    expect(d.name()).toBe('app');
    expect(d.storesVersion).toBe(true);
  });

  it('defaults the schema name to postgres', async () => {
    const d = await openPostgresDriver({
      db: { query: async () => ({ rows: [] }) },
      connect: async () => new FakeClient(),
      table: 't',
    });
    expect(d.name()).toBe('postgres');
  });

  it('rolls back and wraps a failed DDL', async () => {
    const c = new FakeClient();
    c.answers.set(createTableSql('schema_versions'), () => Promise.reject(new Error('boom')));
    await expect(driverOver({ query: async () => ({ rows: [] }) }, c)).rejects.toThrow(
      'postgres upgrade: migrate: boom',
    );
    expect(c.calls.at(-1)?.text).toBe('ROLLBACK');
    expect(c.released).toEqual([undefined]);
  });
});

describe('version, setVersion, backup', () => {
  it('reads "" for no row, the stored value otherwise, and upserts', async () => {
    const seen: Call[] = [];
    let row: unknown[] = [];
    const db: PgQueryable = {
      query: async (text, params) => {
        seen.push({ text, params });
        return { rows: row as PgResult['rows'] };
      },
    };
    const d = await driverOver(db, new FakeClient());
    expect(await d.version()).toBe('');
    row = [{ version: '1.2.0' }];
    expect(await d.version()).toBe('1.2.0');
    await d.setVersion('1.3.0');
    expect(seen[0]).toEqual({ text: versionSql('schema_versions'), params: ['app'] });
    expect(seen[2]).toEqual({ text: upsertSql('schema_versions'), params: ['app', '1.3.0'] });
  });

  it('wraps a failed read', async () => {
    const db: PgQueryable = { query: () => Promise.reject(new Error('down')) };
    const d = await driverOver(db, new FakeClient());
    await expect(d.version()).rejects.toThrow('postgres upgrade: version: down');
    await expect(d.setVersion('1')).rejects.toThrow('postgres upgrade: set version: down');
  });

  it('reports backup and restore unsupported', async () => {
    const d = await driverOver({ query: async () => ({ rows: [] }) }, new FakeClient());
    await expect(d.backup('/x')).rejects.toSatisfy(isBackupUnsupported);
    await expect(d.restore('/x')).rejects.toSatisfy(isBackupUnsupported);
  });
});

describe('lock', () => {
  const db: PgQueryable = { query: async () => ({ rows: [] }) };

  async function lockable(answer: (p?: unknown[]) => PgResult | Promise<PgResult>) {
    const ddl = new FakeClient();
    const lockClient = new FakeClient();
    lockClient.answers.set(SQL_TRY_LOCK, answer);
    lockClient.answers.set(SQL_UNLOCK, () => rows(true));
    let n = 0;
    const d = await openPostgresDriver({
      db,
      connect: async () => (n++ === 0 ? ddl : lockClient),
      table: 'schema_versions',
      name: 'app',
    });
    return { d, lockClient };
  }

  it('takes the key, then unlocks on the same client before releasing it', async () => {
    const { d, lockClient } = await lockable(() => rows(true));
    const unlock = await d.lock();
    expect(lockClient.calls).toEqual([{ text: SQL_TRY_LOCK, params: ['kit-upgrade:schema_versions:app'] }]);
    expect(lockClient.released).toEqual([]);
    await unlock();
    await unlock();
    expect(lockClient.calls.map((c) => c.text)).toEqual([SQL_TRY_LOCK, SQL_UNLOCK]);
    expect(lockClient.calls[1].params).toEqual(['kit-upgrade:schema_versions:app']);
    expect(lockClient.released).toEqual([undefined]);
  });

  it('destroys the client when unlock answers false or fails', async () => {
    for (const bad of [() => rows(false), () => Promise.reject(new Error('gone'))]) {
      const { d, lockClient } = await lockable(() => rows(true));
      lockClient.answers.set(SQL_UNLOCK, bad);
      const unlock = await d.lock();
      await expect(unlock()).rejects.toThrow('postgres upgrade: unlock:');
      expect(lockClient.released).toEqual([true]);
    }
  });

  it('refuses a pool of one before connecting', async () => {
    let connects = 0;
    const one: PgQueryable = { query: async () => ({ rows: [] }), options: { max: 1 } };
    const d = await openPostgresDriver({
      db: one,
      connect: async () => (connects++, new FakeClient()),
      table: 't',
    });
    const before = connects;
    await expect(d.lock()).rejects.toThrow(/at least two connections/);
    expect(connects).toBe(before);
  });

  it('aborted wait rejects with the abort error and destroys the client', async () => {
    const { d, lockClient } = await lockable(() => rows(false));
    const ac = new AbortController();
    const p = d.lock(ac.signal);
    setTimeout(() => ac.abort(), 120);
    const err = await p.then(() => undefined, (e: unknown) => e);
    expect(isCanceled(err)).toBe(true);
    expect((err as Error).message).toBe('context canceled');
    expect(lockClient.released).toEqual([true]);
    expect(lockClient.calls.every((c) => c.text === SQL_TRY_LOCK)).toBe(true);
    expect(lockClient.calls.length).toBeGreaterThan(1);
  });

  it('a signal already aborted connects to nothing', async () => {
    const { d, lockClient } = await lockable(() => rows(true));
    const ac = new AbortController();
    ac.abort();
    await expect(d.lock(ac.signal)).rejects.toSatisfy(isCanceled);
    expect(lockClient.calls).toEqual([]);
  });

  it('a lock won as the abort lands is destroyed, not returned', async () => {
    const ac = new AbortController();
    const { d, lockClient } = await lockable(() => {
      ac.abort();
      return rows(true);
    });
    await expect(d.lock(ac.signal)).rejects.toSatisfy(isCanceled);
    expect(lockClient.released).toEqual([true]);
  });

  it('wraps a connect failure', async () => {
    let n = 0;
    const d = await openPostgresDriver({
      db,
      connect: async () => {
        if (n++ === 0) return new FakeClient();
        throw new Error('refused');
      },
      table: 't',
    });
    await expect(d.lock()).rejects.toThrow('postgres upgrade: lock: refused');
  });

  it('ends a standalone client (no release) through end()', async () => {
    let ended = 0;
    const standalone: PgLockClient = {
      query: async (text) => (text === SQL_UNLOCK || text === SQL_TRY_LOCK ? rows(true) : { rows: [] }),
      end: async () => void ended++,
    };
    const d = await openPostgresDriver({ db, connect: async () => standalone, table: 't' });
    ended = 0; // the DDL client was ended too
    const unlock = await d.lock();
    await unlock();
    expect(ended).toBe(1);
  });
});
