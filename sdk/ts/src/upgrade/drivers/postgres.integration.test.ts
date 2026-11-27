// Integration tests for the Postgres driver against a Postgres 16
// container started from this file (Docker is required: a missing Docker
// fails the run). Run with `make test-ts-upgrade-integration`.

import { fork } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { buildSync } from 'esbuild';
import { Client, Pool } from 'pg';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';

import { startPostgres, type PgContainer } from '../../../test/upgrade/pg-container';
import { isBackupUnsupported, isCanceled, isNotRestored } from '../errors';
import { Registry } from '../migration';
import { Migrator } from '../migrator';
import { openPostgresDriver, type PostgresDriver } from './postgres';

let pg: PgContainer;
let seq = 0;
const cleanups: Array<() => Promise<void> | void> = [];
const warnings: string[] = [];

beforeAll(async () => {
  pg = await startPostgres();
}, 180_000);

afterAll(() => pg?.stop());

afterEach(async () => {
  while (cleanups.length) await cleanups.pop()!();
  warnings.length = 0;
});

const table = (): string => `v_${process.pid}_${++seq}`;

function pool(max = 4): Pool {
  const p = new Pool({ ...pg.config, max });
  cleanups.push(() => p.end());
  return p;
}

async function raw(): Promise<Client> {
  const c = new Client(pg.config);
  await c.connect();
  cleanups.push(() => c.end());
  return c;
}

async function driver(p: Pool, t: string, name = 'app'): Promise<PostgresDriver> {
  return openPostgresDriver({ db: p, connect: () => p.connect(), table: t, name });
}

// The key text the Go driver computes, written out on purpose: these tests
// must break if the TypeScript key ever drifts from Go's.
const goKey = (t: string, name: string): string => `kit-upgrade:${t}:${name}`;

/** Whether a separate session can take the session lock for `key` right now. */
async function otherCanLock(key: string): Promise<boolean> {
  const c = await raw();
  const r = await c.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS ok', [key]);
  const ok = r.rows[0].ok as boolean;
  if (ok) await c.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [key]);
  return ok;
}

function registry(schema: string, steps: Array<[string, () => Promise<void> | void, (() => void) | undefined]>) {
  const reg = new Registry();
  for (const [version, up, down] of steps) reg.register({ version, schema, up, ...(down ? { down } : {}) });
  return reg;
}

function migrator(reg: Registry, opts: { manual?: boolean; baseline?: () => string } = {}): Migrator {
  const dir = mkdtempSync(join(tmpdir(), 'kit-ts-pg-'));
  cleanups.push(() => rmSync(dir, { recursive: true, force: true }));
  return new Migrator('pgtest', '9.9.9', {
    registry: reg,
    dataDir: dir,
    manualRollback: opts.manual,
    baseline: opts.baseline,
    warn: (m) => warnings.push(m),
  });
}

describe('version table', () => {
  it('round-trips a version and upserts', async () => {
    const p = pool();
    const t = table();
    const d = await driver(p, t);
    expect(await d.version()).toBe('');
    await d.setVersion('1.0.0');
    expect(await d.version()).toBe('1.0.0');
    await d.setVersion('1.1.0');
    expect(await d.version()).toBe('1.1.0');
    const other = await driver(p, t, 'other');
    expect(await other.version()).toBe('');
    await other.setVersion('7');
    expect(await d.version()).toBe('1.1.0');
    const rows = await p.query(`SELECT count(*)::int AS n FROM ${t}`);
    expect(rows.rows[0].n).toBe(2);
  });

  it('creates the table once under concurrent opens', async () => {
    const p = pool(8);
    const t = table();
    await Promise.all(Array.from({ length: 6 }, () => driver(p, t)));
  });

  it('never closes the callers pool', async () => {
    const p = pool();
    const t = table();
    const d = await driver(p, t);
    const m = migrator(registry('app', [['1.0.0', () => {}, undefined]]));
    m.addDriver(d);
    await m.run();
    expect((await p.query('SELECT 1 AS one')).rows[0].one).toBe(1);
  });
});

describe('Migrator over the driver', () => {
  it('two real processes racing one database apply each migration once', async () => {
    const work = mkdtempSync(join(tmpdir(), 'kit-ts-pg-race-'));
    cleanups.push(() => rmSync(work, { recursive: true, force: true }));
    const bundle = join(work, 'worker.cjs');
    buildSync({
      entryPoints: [resolve(__dirname, '../../../test/upgrade/pg-race-worker.ts')],
      bundle: true,
      platform: 'node',
      format: 'cjs',
      external: ['pg-native'],
      outfile: bundle,
      logLevel: 'silent',
    });
    const t = table();
    const counts = join(work, 'counts');
    writeFileSync(counts, '');
    const env = {
      ...process.env,
      PGCFG: JSON.stringify(pg.config),
      TABLE: t,
      SCHEMA: 'app',
      COUNTS: counts,
      DATA_DIR: work,
    };
    const kids = [0, 1].map(() => fork(bundle, [], { env, stdio: ['ignore', 'inherit', 'inherit', 'ipc'] }));
    cleanups.push(() => kids.forEach((k) => k.kill()));
    const results = kids.map(
      (k) =>
        new Promise<string>((resolveKid, reject) => {
          k.on('message', (m) => {
            if (m === 'ok' || String(m).startsWith('error')) resolveKid(String(m));
          });
          k.on('error', reject);
          k.on('exit', (code) => code !== 0 && reject(new Error(`worker exited ${code}`)));
        }),
    );
    // Both are connected and have the table before either starts.
    await new Promise((r) => setTimeout(r, 1500));
    kids.forEach((k) => k.send('go'));
    expect(await Promise.all(results)).toEqual(['ok', 'ok']);

    const lines = readFileSync(counts, 'utf8').trim().split('\n');
    for (const v of ['1.1.0', '1.2.0', '1.3.0']) {
      expect(lines.filter((l) => l.endsWith(` ${v}`))).toHaveLength(1);
    }
    expect(new Set(lines.map((l) => l.split(' ')[0])).size).toBe(1); // one process did all the work
    expect(await (await driver(pool(), t)).version()).toBe('1.3.0');
  }, 60_000);

  it('releases the lock after a failed run: a second session acquires at once', async () => {
    const p = pool();
    const t = table();
    const d = await driver(p, t);
    const m = migrator(
      registry('app', [
        ['1.1.0', () => {}, () => {}],
        ['1.2.0', () => { throw new Error('boom'); }, () => {}],
      ]),
    );
    m.addDriver(d);
    await expect(m.run()).rejects.toThrow('boom');
    expect(await otherCanLock(goKey(t, 'app'))).toBe(true);
    expect(await d.version()).toBe(''); // both steps undone by their Down
  });

  it('a failed migration with no Down is not restored and keeps the last step', async () => {
    const p = pool();
    const t = table();
    const d = await driver(p, t);
    await d.setVersion('1.0.0');
    const m = migrator(
      registry('app', [
        ['1.1.0', () => {}, undefined],
        ['1.2.0', () => { throw new Error('boom'); }, undefined],
      ]),
    );
    m.addDriver(d);
    const err = await m.run().then(() => undefined, (e: unknown) => e);
    expect(isNotRestored(err)).toBe(true);
    expect(isBackupUnsupported(err)).toBe(true);
    expect(await d.version()).toBe('1.1.0');
    expect(await otherCanLock(goKey(t, 'app'))).toBe(true);
  });

  it('manual rollbackLatest finds no backup, restores nothing and unlocks', async () => {
    const p = pool();
    const t = table();
    const d = await driver(p, t);
    await d.setVersion('1.0.0');
    const m = migrator(
      registry('app', [
        ['1.1.0', () => {}, undefined],
        ['1.2.0', () => { throw new Error('boom'); }, undefined],
      ]),
      { manual: true },
    );
    m.addDriver(d);
    await expect(m.run()).rejects.toThrow('boom');
    const err = await m.rollbackLatest().then(() => undefined, (e: unknown) => e);
    expect(String((err as Error).message)).toContain('no backups found');
    expect(isNotRestored(err)).toBe(true);
    expect(await d.version()).toBe('1.1.0');
    expect(await otherCanLock(goKey(t, 'app'))).toBe(true);
  });

  it('baselines a pre-existing table and runs an additive column migration', async () => {
    const p = pool();
    const t = table();
    const widgets = `widgets_${process.pid}_${seq}`;
    await p.query(`CREATE TABLE ${widgets} (id INT PRIMARY KEY)`);
    await p.query(`INSERT INTO ${widgets} (id) VALUES (1), (2)`);
    const d = await driver(p, t);
    const m = migrator(
      registry('app', [
        ['1.0.0', () => { throw new Error('baseline schema must not run again'); }, undefined],
        ['1.1.0', async () => void (await p.query(`ALTER TABLE ${widgets} ADD COLUMN color TEXT NOT NULL DEFAULT 'red'`)), undefined],
      ]),
      { baseline: () => '1.0.0' },
    );
    m.addDriver(d);
    await m.run();
    expect(await d.version()).toBe('1.1.0');
    const r = await p.query(`SELECT id, color FROM ${widgets} ORDER BY id`);
    expect(r.rows).toEqual([{ id: 1, color: 'red' }, { id: 2, color: 'red' }]);
    expect(m.history().map((a) => a.version)).toEqual(['1.1.0']);
  });
});

describe('lock', () => {
  it('pooled client: unlock runs before the client goes back to the pool', async () => {
    const p = pool(3);
    const t = table();
    const d = await driver(p, t);
    const unlock = await d.lock();
    expect(await otherCanLock(goKey(t, 'app'))).toBe(false);
    await unlock();
    // Returned to the pool, still connected: the session lock must be gone,
    // or the next user of that pooled connection inherits it for ever.
    expect(p.idleCount).toBeGreaterThan(0);
    expect(await otherCanLock(goKey(t, 'app'))).toBe(true);
    // The pool reuses that very connection: it holds no lock.
    const again = await p.connect();
    const held = await again.query(
      "SELECT count(*)::int AS n FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()",
    );
    expect(held.rows[0].n).toBe(0);
    again.release();
  });

  it('a lock excludes a second run until it is unlocked', async () => {
    const p = pool(4);
    const t = table();
    const a = await driver(p, t);
    const b = await driver(pool(4), t);
    const unlock = await a.lock();
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 250);
    await expect(b.lock(ac.signal)).rejects.toSatisfy(isCanceled);
    let got = false;
    const waiter = b.lock().then(async (un) => {
      got = true;
      await un();
    });
    await new Promise((r) => setTimeout(r, 300));
    expect(got).toBe(false);
    await unlock();
    await waiter;
    expect(got).toBe(true);
  });

  it('an aborted wait holds nothing and returns no connection with a lock', async () => {
    const p = pool(3);
    const t = table();
    const d = await driver(p, t);
    const holder = await raw();
    await holder.query('SELECT pg_advisory_lock(hashtextextended($1, 0))', [goKey(t, 'app')]);

    const ac = new AbortController();
    const began = Date.now();
    setTimeout(() => ac.abort(), 300);
    const err = await d.lock(ac.signal).then(() => undefined, (e: unknown) => e);
    expect(isCanceled(err)).toBe(true);
    expect((err as Error).message).toBe('context canceled');
    expect(Date.now() - began).toBeGreaterThanOrEqual(250);
    expect(p.totalCount).toBe(p.idleCount); // nothing checked out
    const locks = await holder.query(
      "SELECT count(*)::int AS n FROM pg_locks WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()",
    );
    expect(locks.rows[0].n).toBe(0);
    const waiting = await holder.query(
      "SELECT count(*)::int AS n FROM pg_locks WHERE locktype = 'advisory' AND NOT granted",
    );
    expect(waiting.rows[0].n).toBe(0);

    // Once the holder lets go, the next lock is granted.
    await holder.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [goKey(t, 'app')]);
    const unlock = await d.lock();
    await unlock();
  });

  it('a Go-style session on the same table and schema excludes the driver, and the reverse', async () => {
    const p = pool(3);
    const t = table();
    const d = await driver(p, t);
    const key = goKey(t, 'app');

    // Go holds: a blocking pg_advisory_lock on the key text, hashed server side.
    const goSession = await raw();
    await goSession.query('SELECT pg_advisory_lock(hashtextextended($1, 0))', [key]);
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 300);
    await expect(d.lock(ac.signal)).rejects.toSatisfy(isCanceled);
    await goSession.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [key]);

    // TypeScript holds: Go's blocking statement cannot be granted; the lock
    // sits on exactly the 64-bit key the server derives from the Go text.
    const unlock = await d.lock();
    const probe = await raw();
    const k = await probe.query('SELECT hashtextextended($1, 0) AS k', [key]);
    const big = BigInt.asUintN(64, BigInt(k.rows[0].k as string));
    const classid = Number(big >> 32n);
    const objid = Number(big & 0xffffffffn);
    const lk = await probe.query(
      "SELECT classid::bigint AS classid, objid::bigint AS objid, objsubid, mode, granted FROM pg_locks WHERE locktype = 'advisory'",
    );
    expect(lk.rows).toEqual([{ classid: String(classid), objid: String(objid), objsubid: 1, mode: 'ExclusiveLock', granted: true }]);
    await probe.query("SET lock_timeout = '300ms'");
    await expect(probe.query('SELECT pg_advisory_lock(hashtextextended($1, 0))', [key])).rejects.toMatchObject({ code: '55P03' });
    expect((await probe.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS ok', [key])).rows[0].ok).toBe(false);
    await unlock();
    expect((await probe.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS ok', [key])).rows[0].ok).toBe(true);
  });

  it('a pool of one is refused at lock time, and a run reports it instead of hanging', async () => {
    const one = pool(1);
    const t = table();
    const d = await driver(one, t);
    await expect(d.lock()).rejects.toThrow(/at least two connections/);
    const m = migrator(registry('app', [['1.0.0', () => {}, undefined]]));
    m.addDriver(d);
    await expect(m.run()).rejects.toThrow(/migrate app: lock: .*at least two connections/);
    // The pool is untouched and usable.
    expect((await one.query('SELECT 1 AS one')).rows[0].one).toBe(1);
    // Two connections is enough.
    const two = pool(2);
    const m2 = migrator(registry('app', [['1.0.0', () => {}, undefined]]));
    m2.addDriver(await driver(two, t));
    await m2.run();
  });
});
