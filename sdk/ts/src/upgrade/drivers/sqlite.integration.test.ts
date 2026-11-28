// Needs the native better-sqlite3 build: run by `make test-ts-upgrade-integration`.

import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import Database from 'better-sqlite3';
import { buildSync } from 'esbuild';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { tempDir } from '../../../test/upgrade/driver-support';
import { insert, integrity, openWAL, rows } from '../../../test/upgrade/sqlite-support';
import {
  BackupUnsupportedError,
  Migrator,
  Registry,
  type EventPublisher,
  isBackupUnsupported,
  isCanceled,
  isNotRestored,
  readVersionFile,
  type SchemaDriver,
} from '../index';
import { SqliteDriver } from './sqlite';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '../../..');

let tmp: ReturnType<typeof tempDir>;
let dbPath: string;
let data: string;
let db: Database.Database;
beforeEach(() => {
  tmp = tempDir();
  dbPath = join(tmp.dir, 'app.db');
  data = join(tmp.dir, 'data');
  db = openWAL(dbPath); // at 1.0.0, row 1 still in the WAL
  insert(db, 1);
  mkdirSync(join(data, 'appdb'), { recursive: true });
  writeFileSync(join(data, 'appdb', 'version'), '1.0.0');
});
afterEach(() => {
  if (db.open) db.close();
  tmp.cleanup();
});

const driver = (extra: ConstructorParameters<typeof SqliteDriver>[1] = {}): SqliteDriver =>
  new SqliteDriver(dbPath, { name: 'appdb', dataDir: data, ...extra });
const sidecars = (p: string): string[] => ['-wal', '-shm', '-journal'].map((s) => p + s);
const failure = async (p: Promise<unknown>): Promise<Error> =>
  p.then(
    () => {
      throw new Error('expected a rejection');
    },
    (e: unknown) => e as Error,
  );

describe('backup', () => {
  it('holds the rows still in the WAL (handle mode, handle left open)', async () => {
    expect(db.open).toBe(true);
    const dest = join(tmp.dir, 'backup');
    await driver({ db }).backup(dest);
    expect(rows(join(dest, 'app.db'))).toEqual([1]);
    expect(integrity(join(dest, 'app.db'))).toBe('ok');
    expect(db.open).toBe(true);
    insert(db, 2); // still usable
  });

  it('holds the rows still in the WAL (path only, own connection)', async () => {
    const dest = join(tmp.dir, 'backup');
    await driver().backup(dest);
    expect(rows(join(dest, 'app.db'))).toEqual([1]);
    expect(db.open).toBe(true);
  });

  it('is nothing to back up when the file does not exist', async () => {
    const dest = join(tmp.dir, 'backup');
    await new SqliteDriver(join(tmp.dir, 'nope.db')).backup(dest);
    expect(existsSync(join(dest, 'nope.db'))).toBe(false);
  });

  it('replaces a stale target and tmp file', async () => {
    const dest = join(tmp.dir, 'backup');
    mkdirSync(dest);
    writeFileSync(join(dest, 'app.db'), 'stale');
    writeFileSync(join(dest, 'app.db.tmp'), 'stale');
    await driver().backup(dest);
    expect(rows(join(dest, 'app.db'))).toEqual([1]);
    expect(existsSync(join(dest, 'app.db.tmp'))).toBe(false);
  });

  it("binds the target as a parameter: quotes and spaces in a path are fine", async () => {
    const dest = join(tmp.dir, "it's a back?up #1 %");
    await driver().backup(dest);
    expect(rows(join(dest, 'app.db'))).toEqual([1]);
  });

  it('fails clearly inside a caller transaction, handle left open and untouched', async () => {
    db.exec('BEGIN');
    insert(db, 9);
    const err = await failure(driver({ db }).backup(join(tmp.dir, 'backup')));
    expect(err.message).toMatch(/^backup db: .*open transaction.*VACUUM INTO/);
    expect(db.open).toBe(true);
    expect(db.inTransaction).toBe(true);
    db.exec('ROLLBACK');
    expect(existsSync(join(tmp.dir, 'backup', 'app.db'))).toBe(false);
    expect(existsSync(join(tmp.dir, 'backup', 'app.db.tmp'))).toBe(false);
  });
});

describe('restore', () => {
  it('path only: swaps the file and drops stale sidecars', async () => {
    const dest = join(tmp.dir, 'backup');
    await driver().backup(dest);
    insert(db, 2);
    db.close();
    for (const p of sidecars(dbPath)) writeFileSync(p, 'stale');

    await driver().restore(dest);
    expect(rows(dbPath)).toEqual([1]);
    expect(integrity(dbPath)).toBe('ok');
    for (const p of sidecars(dbPath)) expect(existsSync(p), p).toBe(false);
    expect(existsSync(dest + '/app.db')).toBe(true); // the backup stays
  });

  it('path only: keeps the database file mode and leaves no temp file', async () => {
    const dest = join(tmp.dir, 'backup');
    await driver().backup(dest);
    db.close();
    execFileSync('chmod', ['640', dbPath]);
    await driver().restore(dest);
    expect(execFileSync('stat', process.platform === 'darwin' ? ['-f', '%Lp', dbPath] : ['-c', '%a', dbPath]).toString().trim()).toBe('640');
    expect(execFileSync('ls', [tmp.dir]).toString().split('\n').filter((n) => n.includes('.restore-'))).toEqual([]);
  });

  it('path only: a missing backup file fails before touching the database', async () => {
    db.close();
    const err = await failure(driver().restore(join(tmp.dir, 'empty')));
    expect(err.message).toMatch(/^open backup: /);
    expect(rows(dbPath)).toEqual([1]);
  });

  it('caller handle: unsupported, nothing touched, handle left open', async () => {
    const dest = join(tmp.dir, 'backup');
    await driver({ db }).backup(dest);
    insert(db, 2);
    const err = await failure(driver({ db }).restore(dest));
    expect(err).toBeInstanceOf(BackupUnsupportedError);
    expect(isBackupUnsupported(err)).toBe(true);
    expect(db.open).toBe(true);
    expect(rows(dbPath)).toEqual([1, 2]);
    expect(rows(join(dest, 'app.db'))).toEqual([1]);
  });
});

describe('lock', () => {
  it('fits SchemaDriver, lock included', () => {
    const d: SchemaDriver = driver();
    expect(typeof d.lock).toBe('function');
  });

  it('lives in .lock.db beside the version file and is free after unlock', async () => {
    const d = driver();
    const unlock = await d.lock();
    expect(existsSync(join(data, 'appdb', '.lock.db'))).toBe(true);
    await unlock();
    await unlock(); // idempotent
    await (await d.lock(AbortSignal.timeout(2000)))();
    expect(existsSync(join(data, 'appdb', '.lock.db'))).toBe(true); // never deleted
  });

  it('creates the version directory', async () => {
    const d = new SqliteDriver(dbPath, { name: 'fresh', dataDir: data });
    await (await d.lock())();
    expect(existsSync(join(data, 'fresh', '.lock.db'))).toBe(true);
  });

  it('a second lock waits for the first without blocking the event loop', async () => {
    const first = await driver().lock();
    let ticks = 0;
    const timer = setInterval(() => ticks++, 10);
    let got = false;
    const second = driver().lock().then((u) => {
      got = true;
      return u;
    });
    await new Promise((r) => setTimeout(r, 300));
    expect(got).toBe(false);
    expect(ticks).toBeGreaterThanOrEqual(10); // the loop kept running while it waited
    await first();
    const unlock = await second;
    clearInterval(timer);
    expect(got).toBe(true);
    await unlock();
  });

  it('a cancelled wait rejects with the abort error and holds nothing', async () => {
    const first = await driver().lock();
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 100);
    const err = await failure(driver().lock(ac.signal));
    expect(isCanceled(err)).toBe(true);
    expect(err.message).toBe('context canceled');
    await first();
    // The cancelled attempt left no lock behind.
    await (await driver().lock(AbortSignal.timeout(2000)))();

    const done = new AbortController();
    done.abort();
    expect(isCanceled(await failure(driver().lock(done.signal)))).toBe(true);
  });
});

describe('two processes', () => {
  it('racing one file apply each migration once', async () => {
    const dir = join(tmp.dir, 'race');
    mkdirSync(dir);
    const racePath = join(dir, 'app.db');
    const setup = openWAL(racePath);
    setup.exec('CREATE TABLE applied (version TEXT, pid INTEGER)');
    setup.close();

    const bundle = join(tmp.dir, 'race-helper.cjs');
    buildSync({
      entryPoints: [join(ROOT, 'test/upgrade/race-helper.ts')],
      bundle: true,
      platform: 'node',
      format: 'cjs',
      external: ['better-sqlite3'],
      outfile: bundle,
      logLevel: 'silent',
    });
    const env = { ...process.env, RACE_DIR: dir, NODE_PATH: join(ROOT, 'node_modules') };
    const run = (): Promise<{ code: number | null; out: string }> =>
      new Promise((resolve) => {
        const p = spawn(process.execPath, [bundle], { env, cwd: ROOT });
        let out = '';
        p.stdout.on('data', (b: Buffer) => (out += b));
        p.stderr.on('data', (b: Buffer) => (out += b));
        p.on('close', (code) => resolve({ code, out }));
      });
    const results = await Promise.all([run(), run()]);
    for (const r of results) expect(r, r.out).toMatchObject({ code: 0 });

    const check = new Database(racePath, { readonly: true });
    const got: Record<string, number> = {};
    for (const r of check.prepare('SELECT version FROM applied').all() as { version: string }[]) {
      got[r.version] = (got[r.version] ?? 0) + 1;
    }
    check.close();
    expect(got).toEqual({ '1.1.0': 1, '1.2.0': 1 });
    expect(await readVersionFile(join(dir, 'data'), 'appdb')).toBe('1.2.0');
  }, 30_000);
});

// Collects the method of every reverted event.
function reverts(): EventPublisher & { methods: string[] } {
  const methods: string[] = [];
  return {
    methods,
    publish(e) {
      if (e.topic.endsWith('.reverted')) methods.push((e.payload as { method: string }).method);
    },
  };
}
function addColumn(path: string, col: string): void {
  const c = new Database(path);
  try {
    c.exec(`ALTER TABLE t ADD COLUMN ${col} TEXT`);
  } finally {
    c.close();
  }
}
function columns(path: string): string[] {
  const c = new Database(path, { readonly: true, fileMustExist: true });
  try {
    return (c.prepare("SELECT name FROM pragma_table_info('t') ORDER BY cid").all() as { name: string }[]).map((r) => r.name);
  } finally {
    c.close();
  }
}
// Backup directories only: `pre-<v>.version` sidecars sit beside them.
const backupDirs = (): string[] =>
  readdirSync(join(data, 'backups', 'appdb'), { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
    .sort();

// End to end: 1.1.0 inserts row 2; 1.2.0 fails (after `before12`).
// With `finalVerify`, 1.2.0 applies (inserting row 3) and the final verify
// fails instead. `downs` collects the versions whose down ran.
function migrator(
  withDown: boolean,
  d: SqliteDriver,
  before12?: () => void,
  manual = false,
  finalVerify = false,
  publisher?: EventPublisher,
  downs: string[] = [],
): Migrator {
  const reg = new Registry();
  reg.register({
    version: '1.1.0',
    schema: 'appdb',
    up: () => void db.prepare('INSERT INTO t (x) VALUES (2)').run(),
    down: withDown
      ? () => {
          downs.push('1.1.0');
          db.prepare('DELETE FROM t WHERE x = 2').run();
        }
      : undefined,
  });
  reg.register({
    version: '1.2.0',
    schema: 'appdb',
    up: () => {
      before12?.();
      if (finalVerify) return void db.prepare('INSERT INTO t (x) VALUES (3)').run();
      throw new Error('simulated failure');
    },
    down: withDown
      ? () => {
          downs.push('1.2.0');
          db.prepare('DELETE FROM t WHERE x = 3').run();
        }
      : undefined,
  });
  const m = new Migrator('tool', '1.2.0', {
    registry: reg,
    dataDir: data,
    manualRollback: manual,
    warn: () => {},
    publisher,
    verify: finalVerify
      ? () => {
          throw new Error('simulated failure');
        }
      : undefined,
  });
  m.addDriver(d);
  return m;
}
const unlocked = async (d: SqliteDriver): Promise<void> => {
  await (await d.lock(AbortSignal.timeout(2000)))();
};

describe('through the Migrator', () => {
  it('path only: a failed run restores the file; the lock is free afterwards', async () => {
    const d = driver();
    const err = await failure(migrator(false, d, () => db.close()).run());
    expect(err.message).toContain('simulated failure');
    expect(isNotRestored(err)).toBe(false);
    expect(rows(dbPath)).toEqual([1]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.0.0');
    expect(existsSync(join(data, 'appdb', '.lock.db'))).toBe(true);
    await unlocked(d);
  });

  it('caller handle, no down: ends not restored; backup stays; handle open', async () => {
    const d = driver({ db });
    const err = await failure(migrator(false, d).run());
    expect(isNotRestored(err)).toBe(true);
    expect(isBackupUnsupported(err)).toBe(true);
    expect(err.message).toContain('simulated failure');
    expect(await readVersionFile(data, 'appdb')).toBe('1.1.0');
    expect(rows(dbPath)).toEqual([1, 2]);
    expect(rows(join(data, 'backups', 'appdb', 'pre-1.1.0', 'app.db'))).toEqual([1]);
    expect(db.open).toBe(true);
    await unlocked(d);
  });

  it('caller handle, with down: a failed final verify rolls back by down', async () => {
    const m = migrator(true, driver({ db }), undefined, false, true);
    const err = await failure(m.run());
    expect(err.message).toContain('final verify 1.2.0 failed: simulated failure');
    expect(isNotRestored(err)).toBe(false);
    expect(rows(dbPath)).toEqual([1]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.0.0');
    expect(db.open).toBe(true);
  });

  it('caller handle, a failed up is not downed: not restored even with every down', async () => {
    const downs: string[] = [];
    const pub = reverts();
    const d = driver({ db });
    const m = migrator(true, d, undefined, false, false, pub, downs);
    const err = await failure(m.run());
    expect(isNotRestored(err)).toBe(true);
    expect(err.message).toContain('up 1.2.0 failed: simulated failure');
    expect(downs).toEqual([]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.1.0');
    expect(rows(dbPath)).toEqual([1, 2]);
    expect(pub.methods).toEqual([]);
    expect(db.open).toBe(true);
    await unlocked(d);
  });

  it('path only: a first up that fails after a partial change is restored', async () => {
    const reg = new Registry();
    reg.register({
      version: '1.1.0',
      schema: 'appdb',
      up: () => {
        db.close(); // precondition of a path-only restore: no handle on the file
        addColumn(dbPath, 'note');
        throw new Error('boom');
      },
    });
    const pub = reverts();
    const d = driver();
    const m = new Migrator('tool', '1.1.0', { registry: reg, dataDir: data, publisher: pub, warn: () => {} });
    m.addDriver(d);
    const err = await failure(m.run());
    expect(err.message).toContain('up 1.1.0 failed: boom');
    expect(isNotRestored(err)).toBe(false);
    expect(columns(dbPath)).not.toContain('note');
    expect(rows(dbPath)).toEqual([1]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.0.0');
    expect(backupDirs()).toEqual(['pre-1.1.0']);
    expect(pub.methods).toEqual(['restore']);
    await unlocked(d);
  });

  it('path only: a later up that fails restores without calling any down', async () => {
    const downs: string[] = [];
    const reg = new Registry();
    reg.register({
      version: '1.1.0',
      schema: 'appdb',
      up: () => void db.prepare('INSERT INTO t (x) VALUES (2)').run(),
      down: () => void downs.push('1.1.0'),
    });
    reg.register({
      version: '1.2.0',
      schema: 'appdb',
      up: () => {
        db.close();
        addColumn(dbPath, 'note');
        throw new Error('boom');
      },
      down: () => void downs.push('1.2.0'),
    });
    const pub = reverts();
    const m = new Migrator('tool', '1.2.0', { registry: reg, dataDir: data, publisher: pub, warn: () => {} });
    m.addDriver(driver());
    const err = await failure(m.run());
    expect(err.message).toContain('up 1.2.0 failed: boom');
    expect(isNotRestored(err)).toBe(false);
    expect(downs).toEqual([]);
    expect(columns(dbPath)).not.toContain('note');
    expect(rows(dbPath)).toEqual([1]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.0.0');
    expect(pub.methods).toEqual(['restore']);
  });

  it('manual rollback, path only: restores and drops the sidecars', async () => {
    const d = driver();
    const m = migrator(false, d, undefined, true);
    expect((await failure(m.run())).message).toContain('simulated failure');
    expect(await readVersionFile(data, 'appdb')).toBe('1.1.0');
    db.close(); // precondition: no handle on the file
    await m.rollbackLatest();
    expect(rows(dbPath)).toEqual([1]);
    expect(await readVersionFile(data, 'appdb')).toBe('1.0.0');
    for (const p of sidecars(dbPath)) expect(existsSync(p), p).toBe(false);
    await unlocked(d);
  });

  it('manual rollback, caller handle: not restored, version and handle stay', async () => {
    const d = driver({ db });
    const m = migrator(false, d, undefined, true);
    await failure(m.run());
    const err = await failure(m.rollbackLatest());
    expect(isNotRestored(err)).toBe(true);
    expect(isBackupUnsupported(err)).toBe(true);
    expect(await readVersionFile(data, 'appdb')).toBe('1.1.0');
    expect(rows(dbPath)).toEqual([1, 2]);
    expect(db.open).toBe(true);
    await unlocked(d);
  });

  it('a successful run in handle mode leaves a backup with the WAL rows', async () => {
    const reg = new Registry();
    reg.register({ version: '1.1.0', schema: 'appdb', up: () => void db.prepare('INSERT INTO t (x) VALUES (2)').run() });
    const m = new Migrator('tool', '1.1.0', { registry: reg, dataDir: data });
    m.addDriver(driver({ db }));
    await m.run();
    expect(rows(join(data, 'backups', 'appdb', 'pre-1.1.0', 'app.db'))).toEqual([1]);
    expect(rows(dbPath)).toEqual([1, 2]);
    expect(readFileSync(join(data, 'appdb', 'version'), 'utf8')).toBe('1.1.0');
  });
});
