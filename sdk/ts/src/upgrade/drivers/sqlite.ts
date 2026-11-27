/**
 * A {@link SchemaDriver} for a SQLite database file, ported from Go
 * `core/upgrade/driver/sqlite`, over `better-sqlite3`.
 *
 * Two modes. Path only: `new SqliteDriver(dbPath)` opens its own
 * short-lived connection for `backup` and swaps the file on `restore`.
 * Handle: `new SqliteDriver(dbPath, { db })` backs up through the caller's
 * `Database`, never closes it, and cannot restore (the file cannot be
 * replaced under a live connection): `restore` throws
 * {@link BackupUnsupportedError} and the backup stays for manual recovery.
 *
 * The lock is `BEGIN EXCLUSIVE` on a separate database `.lock.db` beside
 * the version file, because Node has no `flock`. It excludes other
 * connections and processes using this driver, but does NOT interoperate
 * with the `flock` on `.lock` that the Go, Python and Rust drivers take: a
 * TypeScript and a non-TypeScript process migrating one file do not
 * exclude each other.
 *
 * Imports `better-sqlite3`: import this module only where it is used
 * (`@hop-top/kit/upgrade/drivers/sqlite`); the main entry never loads it.
 *
 * @packageDocumentation
 */

import { promises as fs } from 'node:fs';
import { randomBytes } from 'node:crypto';
import * as path from 'node:path';

import Database from 'better-sqlite3';

import { BackupUnsupportedError, abortError } from '../errors';
import { resolveDataDir, versionOf, type DriverOptions } from './version';

/** Options of a {@link SqliteDriver}. */
export interface SqliteOptions extends DriverOptions {
  /** The schema name (default `"sqlite"`). */
  name?: string;
  /**
   * The caller's open connection. `backup` runs on it and leaves it open;
   * `restore` throws {@link BackupUnsupportedError}. `dbPath` then only
   * names the backup file (and may be empty).
   */
  db?: Database.Database;
}

/** Files SQLite keeps beside a database; a stale -wal or -journal would be replayed onto a restored one. */
const SIDECARS = ['-wal', '-shm', '-journal'];
/** How long the driver's own backup connection waits on a lock. */
const BUSY_TIMEOUT_MS = 5000;
/** The lock database, in the version file's directory. */
export const LOCK_FILE = '.lock.db';
/** How often a blocked `lock` retries. */
const LOCK_POLL_MS = 50;

const isMissing = (e: unknown): boolean => (e as NodeJS.ErrnoException).code === 'ENOENT';
const msg = (e: unknown): string => (e instanceof Error ? e.message : String(e));
const isBusy = (e: unknown): boolean => {
  const code = (e as { code?: unknown }).code;
  return typeof code === 'string' && (code.startsWith('SQLITE_BUSY') || code.startsWith('SQLITE_LOCKED'));
};

/**
 * Backs up, restores and locks one SQLite database file.
 *
 * A `SchemaDriver`; not declared with `implements` so the bundled declarations
 * keep `SchemaDriver` out of a chunk shared with the root barrel (the tests
 * check the fit).
 */
export class SqliteDriver {
  private readonly schema: string;
  private readonly db?: Database.Database;
  private readonly opts: DriverOptions;

  constructor(
    private readonly dbPath: string,
    options: SqliteOptions = {},
  ) {
    this.schema = options.name ?? 'sqlite';
    this.db = options.db;
    this.opts = { dataDir: options.dataDir, tool: options.tool };
  }

  name(): string {
    return this.schema;
  }

  version(): Promise<string> {
    return versionOf(this.opts, this.schema);
  }

  /** The Migrator writes the version file; nothing to do here. */
  setVersion(): void {}

  /** The backup file's name: the database file's base name, or `<schema>.db` without a path. */
  private backupName(): string {
    return this.dbPath !== '' ? path.basename(this.dbPath) : this.schema + '.db';
  }

  /**
   * Writes a consistent snapshot to `<dest>/<database file name>` with
   * `VACUUM INTO`, which reads through the WAL: rows committed but not yet
   * checkpointed are in it. Path only: a missing database file is
   * "nothing to back up" (no file, no error); the driver opens a
   * connection of its own and closes it. With a caller handle the snapshot
   * runs on it, left open; it must not be inside a transaction.
   */
  async backup(dest: string): Promise<void> {
    if (this.db === undefined) {
      try {
        await fs.stat(this.dbPath);
      } catch (e) {
        if (isMissing(e)) return;
        throw new Error(`stat db: ${msg(e)}`, { cause: e });
      }
    }
    try {
      await fs.mkdir(dest, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create backup dir: ${msg(e)}`, { cause: e });
    }
    const target = path.join(dest, this.backupName());
    const tmp = target + '.tmp';
    // VACUUM INTO refuses an existing file; a leftover is stale.
    try {
      await fs.rm(tmp, { force: true });
      await fs.rm(target, { force: true });
    } catch (e) {
      throw new Error(`clear backup file: ${msg(e)}`, { cause: e });
    }
    try {
      this.vacuumInto(tmp);
      await fs.rename(tmp, target);
    } catch (e) {
      await fs.rm(tmp, { force: true });
      throw new Error(`backup db: ${msg(e)}`, { cause: e });
    }
  }

  /** Snapshots the database to `file`, bound as a parameter so no path needs quoting. */
  private vacuumInto(file: string): void {
    if (this.db !== undefined) {
      if (this.db.inTransaction) {
        throw new Error(
          "the caller's connection has an open transaction; VACUUM INTO cannot run inside one: commit or roll it back first",
        );
      }
      this.db.prepare('VACUUM INTO ?').run(file);
      return;
    }
    const own = new Database(this.dbPath, { fileMustExist: true, timeout: BUSY_TIMEOUT_MS });
    try {
      own.prepare('VACUUM INTO ?').run(file);
    } finally {
      own.close();
    }
  }

  /**
   * Replaces the database file with the backup in `src`, then removes the
   * replaced database's `-wal`, `-shm` and `-journal` files.
   *
   * Precondition: no open handle on the file, in this process or another;
   * a connection left open keeps using the replaced file and may recreate
   * its -wal beside the restored one. With a caller handle it restores
   * nothing and throws {@link BackupUnsupportedError}; the backup stays.
   *
   * The backup is copied to a temporary file beside the database, synced
   * and renamed over it, so a failure before the rename leaves the
   * database and its sidecars untouched. An error after the rename means
   * the database already holds the backup; a sidecar named in the error
   * must be removed before the database is opened.
   */
  async restore(src: string): Promise<void> {
    if (this.db !== undefined) {
      throw new BackupUnsupportedError(
        `restore ${this.dbPath} under a caller-owned handle: upgrade: backup unsupported`,
      );
    }
    const backup = path.join(src, this.backupName());
    try {
      await fs.stat(backup);
    } catch (e) {
      throw new Error(`open backup: ${msg(e)}`, { cause: e });
    }

    const dir = path.dirname(this.dbPath);
    try {
      await fs.mkdir(dir, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create db dir: ${msg(e)}`, { cause: e });
    }
    let mode = 0o600;
    try {
      mode = (await fs.stat(this.dbPath)).mode & 0o777;
    } catch {
      // No database yet: private by default.
    }
    const tmp = `${this.dbPath}.restore-${randomBytes(6).toString('hex')}`;
    try {
      await fs.copyFile(backup, tmp, fs.constants.COPYFILE_EXCL);
      await fs.chmod(tmp, mode);
      await syncFile(tmp);
      await fs.rename(tmp, this.dbPath);
    } catch (e) {
      await fs.rm(tmp, { force: true });
      throw new Error(`restore db: ${msg(e)}`, { cause: e });
    }

    const errs: string[] = [];
    for (const suffix of SIDECARS) {
      const p = this.dbPath + suffix;
      try {
        await fs.rm(p);
      } catch (e) {
        if (!isMissing(e)) errs.push(`remove stale ${p}: ${msg(e)}`);
      }
    }
    if (process.platform !== 'win32') {
      try {
        await syncFile(dir);
      } catch (e) {
        errs.push(`fsync dir ${dir}: ${msg(e)}`);
      }
    }
    if (errs.length > 0) throw new Error(`restore: ${this.dbPath} replaced: ${errs.join('; ')}`);
  }

  /** `<data dir>/<schema>`: where the version file and the lock database live. */
  private stateDir(): string {
    return path.join(resolveDataDir(this.opts), this.schema);
  }

  /**
   * Holds `BEGIN EXCLUSIVE` on `<version dir>/.lock.db` (the directory is
   * created if needed) until the returned function runs, retrying about
   * every 50 ms while another connection holds it. When `signal` aborts
   * while waiting it rejects with `abortError(signal)` and holds nothing.
   * The retry loop is asynchronous (the busy timeout is 0), so a wait
   * never blocks the event loop. The file is never deleted. Does not
   * interoperate with the other ports' `flock` on `.lock`.
   */
  async lock(signal?: AbortSignal): Promise<() => void> {
    const dir = this.stateDir();
    try {
      await fs.mkdir(dir, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create lock dir: ${msg(e)}`, { cause: e });
    }
    const file = path.join(dir, LOCK_FILE);
    const conn = new Database(file, { timeout: 0 });
    try {
      for (;;) {
        if (signal?.aborted) throw abortError(signal);
        try {
          conn.exec('BEGIN EXCLUSIVE');
          break;
        } catch (e) {
          if (!isBusy(e)) throw new Error(`lock ${file}: ${msg(e)}`, { cause: e });
        }
        await sleep(LOCK_POLL_MS, signal);
      }
    } catch (e) {
      conn.close();
      throw e;
    }
    let released = false;
    return () => {
      if (released) return;
      released = true;
      try {
        if (conn.inTransaction) conn.exec('ROLLBACK');
      } finally {
        conn.close();
      }
    };
  }
}

/** Resolves after `ms`, or as soon as `signal` aborts. */
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const done = (): void => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal?.addEventListener('abort', done, { once: true });
  });
}

/** fsyncs a file or a directory. */
async function syncFile(p: string): Promise<void> {
  const h = await fs.open(p, 'r');
  try {
    await h.sync();
  } finally {
    await h.close();
  }
}
