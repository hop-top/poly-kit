/**
 * The migration runner, ported from Go `core/upgrade/migrate.go`: per
 * schema driver, lock, read the version, baseline, pending selection,
 * backup, each `up` then `verify` then the version record, final verify,
 * rollback, prune, unlock.
 */

import * as path from 'node:path';

import {
  BackupStore,
  DEFAULT_RETENTION,
  defaultDataDir,
  writeVersionFile,
} from './backup';
import type { SchemaDriver } from './driver';
import {
  NotRestoredError,
  UpgradeError,
  VerifyError,
  abortError,
  errorText,
  isBackupUnsupported,
} from './errors';
import {
  emit,
  millisSince,
  resolveTopics,
  type MigrationPayload,
  type MigrationRunPayload,
  type MigrationTopics,
} from './events';
import { defaultRegistry, type Migration, type Registry } from './migration';
import type { AppliedMigration, MigratorOptions, SchemaStatus } from './options';

const ERR_NO_BACKUP = 'not restored: no backup was taken';
const ERR_RESTORE_UNSUPPORTED = 'not restored: restore unsupported';

type Stage = 'up' | 'verify' | 'record' | 'final' | 'canceled';

/** What stopped a schema's run, with what to roll back. */
interface StepFailure {
  stage: Stage;
  version: string;
  err: unknown;
  undo: Migration[];
}

/** The backup a schema's run can restore from: a path, or none with why. */
interface BackupState {
  path: string;
  unsupported?: unknown;
}

const asError = (e: unknown): Error => (e instanceof Error ? e : new Error(String(e), { cause: e }));

function recordError(version: string, err: unknown): UpgradeError {
  return new UpgradeError(`record ${version === '' ? '""' : version} failed: ${errorText(err)}`, err);
}

function wrapFailure(f: StepFailure): UpgradeError {
  const text = errorText(f.err);
  switch (f.stage) {
    case 'verify':
      return new UpgradeError(`verify ${f.version} failed: ${text}`, new VerifyError(f.err));
    case 'record':
      return recordError(f.version, f.err);
    case 'final':
      return new UpgradeError(`final verify ${f.version} failed: ${text}`, new VerifyError(f.err));
    case 'canceled':
      return new UpgradeError(`up ${f.version} canceled: ${text}`, f.err);
    default:
      return new UpgradeError(`up ${f.version} failed: ${text}`, f.err);
  }
}

/** Orchestrates versioned migrations across schema drivers. */
export class Migrator {
  private readonly drivers: SchemaDriver[] = [];
  private readonly applied: AppliedMigration[] = [];
  private readonly target: string;
  private readonly opts: MigratorOptions;
  private readonly registry: Registry;
  private readonly topics: MigrationTopics;
  private readonly warn: (message: string) => void;

  constructor(
    readonly tool: string,
    version: string,
    options: MigratorOptions = {},
  ) {
    this.target = version.startsWith('v') ? version.slice(1) : version;
    this.opts = options;
    this.registry = options.registry ?? defaultRegistry();
    this.topics = resolveTopics(options.topicPrefix, options.topics);
    this.warn = options.warn ?? ((m) => console.warn(m));
  }

  /** The target version, without a leading `v`. */
  get version(): string {
    return this.target;
  }

  /** Registers a schema driver; drivers run in the order added. */
  addDriver(d: SchemaDriver): void {
    this.drivers.push(d);
  }

  /** Migrations applied by this Migrator so far, cumulative. */
  history(): AppliedMigration[] {
    return this.applied.map((a) => ({ ...a }));
  }

  /**
   * Executes pending migrations for every driver. Per schema: take the
   * driver's lock (held until the schema's run ends, rollback included),
   * read the version (when none is stored, ask `baseline` and record its
   * answer), select pending, back up (and save the version the run starts
   * at beside it), then per migration `up`, `verify` and the version
   * record, then the final verify. An unsupported backup lets the run go
   * on with no backup. On failure in auto mode: roll back (`down` in
   * reverse, recording the version before each, else `restore` and record
   * the starting version). When no backup can restore the schema nothing
   * is reverted and the error matches `isNotRestored`. An aborted `signal`
   * stops before the next `up` (`up <v> canceled: context canceled`).
   * Rejects with the first failing schema's error, `migrate <schema>: ...`.
   */
  async run(signal?: AbortSignal): Promise<void> {
    const start = performance.now();
    emit(this.opts.publisher, this.topics.runStarted, {
      tool: this.tool,
      to: this.target,
    } satisfies MigrationRunPayload);
    let applied = 0;
    for (const d of this.drivers) {
      try {
        applied += await this.runSchema(d, signal);
      } catch (e) {
        const err = new UpgradeError(`migrate ${d.name()}: ${errorText(e)}`, e);
        emit(this.opts.publisher, this.topics.runFailed, {
          tool: this.tool,
          to: this.target,
          schema: d.name(),
          error: err.message,
          duration_ms: millisSince(start),
        } satisfies MigrationRunPayload);
        throw err;
      }
    }
    emit(this.opts.publisher, this.topics.runCompleted, {
      tool: this.tool,
      to: this.target,
      applied,
      duration_ms: millisSince(start),
    } satisfies MigrationRunPayload);
  }

  /** Pending migration counts per driver. Takes no lock and never baselines. */
  async status(): Promise<SchemaStatus[]> {
    const out: SchemaStatus[] = [];
    for (const d of this.drivers) {
      let current = '';
      let error: Error | undefined;
      try {
        current = await d.version();
      } catch (e) {
        error = asError(e);
      }
      const st: SchemaStatus = {
        schema: d.name(),
        current,
        target: this.target,
        pending: this.registry.pending(d.name(), current, this.target).length,
      };
      if (error) st.error = error;
      out.push(st);
    }
    return out;
  }

  /**
   * Restores the latest backup of each driver, then records the version
   * the run that took it started at (saved beside the backup). A backup
   * without that record is still restored, the version is left as it is
   * and the error says so. A driver with `lock` is locked while its schema
   * is rolled back (lock, backup lookup, restore, record, unlock); the
   * version is not read and no events are published. A schema with no
   * backup, or whose `restore` is unsupported, fails with an error that
   * matches `isNotRestored`; nothing is recorded for it. Manual mode only.
   */
  async rollbackLatest(): Promise<void> {
    if (!this.opts.manualRollback) {
      throw new Error('rollback: only available in manual rollback mode');
    }
    for (const d of this.drivers) {
      try {
        await this.rollbackLatestOne(d);
      } catch (e) {
        throw new UpgradeError(`rollback ${d.name()}: ${errorText(e)}`, e);
      }
    }
  }

  private baseDir(): string {
    if (this.opts.dataDir) return this.opts.dataDir;
    try {
      return defaultDataDir(this.tool);
    } catch (e) {
      throw new Error(`resolve data dir: ${errorText(e)}`, { cause: e });
    }
  }

  private backups(schema: string): BackupStore {
    return new BackupStore(schema, this.baseDir(), this.opts.backupRetention ?? DEFAULT_RETENTION);
  }

  /**
   * Takes the driver's lock when it has one and returns what releases it.
   * The release never throws: a failed unlock is a warning.
   */
  private async lockDriver(d: SchemaDriver, signal?: AbortSignal): Promise<() => Promise<void>> {
    if (typeof d.lock !== 'function') return async () => {};
    let unlock: Awaited<ReturnType<NonNullable<SchemaDriver['lock']>>>;
    try {
      unlock = await d.lock(signal);
    } catch (e) {
      throw new UpgradeError(`lock: ${errorText(e)}`, e);
    }
    return async () => {
      try {
        await unlock();
      } catch (e) {
        this.warn(`warning: unlock ${d.name()}: ${errorText(e)}`);
      }
    };
  }

  private async rollbackLatestOne(d: SchemaDriver): Promise<void> {
    const release = await this.lockDriver(d);
    try {
      const store = this.backups(d.name());
      const latest = await store.latestBackup();
      const { version: preRun, recorded } = await store.readPreRun(latest);
      try {
        await d.restore(latest);
      } catch (e) {
        throw isBackupUnsupported(e) ? new NotRestoredError(ERR_RESTORE_UNSUPPORTED, { cause: e }) : e;
      }
      if (!recorded) {
        throw new Error(`restored ${path.basename(latest)} but it records no pre-run version; version left as is`);
      }
      try {
        await this.setVersion(d, preRun);
      } catch (e) {
        throw recordError(preRun, e);
      }
    } finally {
      await release();
    }
  }

  private async runSchema(d: SchemaDriver, signal?: AbortSignal): Promise<number> {
    const release = await this.lockDriver(d, signal);
    try {
      return await this.runLocked(d, signal);
    } finally {
      await release();
    }
  }

  private async runLocked(d: SchemaDriver, signal?: AbortSignal): Promise<number> {
    let current: string;
    try {
      current = await d.version();
    } catch (e) {
      throw new UpgradeError(`read version: ${errorText(e)}`, e);
    }
    if (current === '' && this.opts.baseline) current = await this.stampBaseline(d, signal);

    const pending = this.registry.pending(d.name(), current, this.target);
    if (pending.length === 0) return 0;

    const store = this.backups(d.name());
    const bk = await this.takeBackup(store, d, pending[0].version, current);

    const { fail, n } = await this.applyAll(d, current, pending, signal);
    if (fail) throw await this.fail(d, current, bk, fail);

    // Prune on success, non-fatal; a schema with no backup this run is left alone.
    if (bk.path !== '') {
      try {
        await store.prune();
      } catch (e) {
        this.warn(`warning: prune old backups for ${d.name()}: ${errorText(e)}`);
      }
    }
    return n;
  }

  /**
   * Makes the backup directory (named after the first pending version),
   * has the driver fill it and saves beside it the version the run starts
   * at. A backup that is unsupported is no failure: the directory this
   * call created is removed again, no version is saved and the run goes on.
   */
  private async takeBackup(store: BackupStore, d: SchemaDriver, first: string, current: string): Promise<BackupState> {
    const existed = await store.hasBackup(first);
    let dir: string;
    try {
      dir = await store.backupDir(first);
    } catch (e) {
      throw new UpgradeError(`prepare backup: ${errorText(e)}`, e);
    }
    try {
      await d.backup(dir);
    } catch (e) {
      if (!isBackupUnsupported(e)) throw new UpgradeError(`backup: ${errorText(e)}`, e);
      if (!existed) await store.discard(dir);
      return { path: '', unsupported: e };
    }
    try {
      await store.savePreRun(dir, current);
    } catch (e) {
      throw new UpgradeError(`prepare backup: ${errorText(e)}`, e);
    }
    return { path: dir };
  }

  /** Asks `baseline` what the unversioned store is at and records a non-empty answer. */
  private async stampBaseline(d: SchemaDriver, signal?: AbortSignal): Promise<string> {
    let v: string | undefined;
    try {
      v = await this.opts.baseline!(d.name(), signal);
    } catch (e) {
      throw new UpgradeError(`baseline: ${errorText(e)}`, e);
    }
    if (v === undefined || v === '') return '';
    try {
      await this.setVersion(d, v);
    } catch (e) {
      const r = recordError(v, e);
      throw new UpgradeError(`baseline: ${r.message}`, r);
    }
    emit(this.opts.publisher, this.topics.baselined, {
      tool: this.tool,
      schema: d.name(),
      from: '',
      to: v,
    } satisfies MigrationPayload);
    return v;
  }

  /** `up`, `verify` and the version record per pending migration, then the final verify. */
  private async applyAll(
    d: SchemaDriver,
    current: string,
    pending: Migration[],
    signal?: AbortSignal,
  ): Promise<{ fail?: StepFailure; n: number }> {
    const schema = d.name();
    const pub = this.opts.publisher;
    const done: Migration[] = [];
    let from = current;
    const failed = (ev: MigrationPayload, t: number | undefined, stage: Stage, err: unknown, undo: Migration[]) => {
      const payload: MigrationPayload = { ...ev, stage, error: errorText(err) };
      if (t !== undefined) payload.duration_ms = millisSince(t);
      emit(pub, this.topics.failed, payload);
      return { fail: { stage, version: ev.to, err, undo }, n: done.length };
    };
    for (const mig of pending) {
      const ev: MigrationPayload = { tool: this.tool, schema, from, to: mig.version };
      if (signal?.aborted) return failed(ev, undefined, 'canceled', abortError(signal), done);
      emit(pub, this.topics.started, ev);
      let t = performance.now();
      try {
        await mig.up(signal);
      } catch (e) {
        return failed(ev, t, 'up', e, done);
      }
      emit(pub, this.topics.applied, { ...ev, duration_ms: millisSince(t) });
      if (mig.verify) {
        t = performance.now();
        try {
          await mig.verify(signal);
        } catch (e) {
          // up completed: the rollback must undo this step too
          return failed(ev, t, 'verify', e, [...done, mig]);
        }
        emit(pub, this.topics.verified, { ...ev, duration_ms: millisSince(t) });
      }
      t = performance.now();
      try {
        await this.setVersion(d, mig.version);
      } catch (e) {
        return failed(ev, t, 'record', e, [...done, mig]);
      }
      done.push(mig);
      this.applied.push({ version: mig.version, schema: mig.schema });
      from = mig.version;
    }
    if (this.opts.verify) {
      const ev: MigrationPayload = { tool: this.tool, schema, from: current, to: from };
      const t = performance.now();
      try {
        await this.opts.verify(schema, from, signal);
      } catch (e) {
        return failed(ev, t, 'final', e, done);
      }
      emit(pub, this.topics.verified, { ...ev, duration_ms: millisSince(t), final: true });
    }
    return { n: done.length };
  }

  /** Rolls back (auto mode) and builds the schema's error. */
  private async fail(d: SchemaDriver, current: string, bk: BackupState, f: StepFailure): Promise<Error> {
    const err = wrapFailure(f);
    if (this.opts.manualRollback || f.undo.length === 0) return err;
    const t = performance.now();
    let method: string;
    try {
      method = await this.rollback(d, current, f.undo, bk);
    } catch (rollbackErr) {
      return new UpgradeError(`${err.message} (rollback also failed: ${errorText(rollbackErr)})`, err, [
        err,
        rollbackErr,
      ]);
    }
    emit(this.opts.publisher, this.topics.reverted, {
      tool: this.tool,
      schema: d.name(),
      from: f.version,
      to: current,
      method,
      duration_ms: millisSince(t),
    } satisfies MigrationPayload);
    return err;
  }

  /**
   * Runs `down` in reverse order, recording the version before each
   * migration once its `down` succeeded; falls back to `restore` (and
   * records the starting version) if a `down` is missing or fails. Returns
   * which of the two undid the changes. A failed record stops the
   * rollback. With no backup, or a `restore` that is unsupported, nothing
   * is restored: a NotRestoredError, and the version stays at the last
   * record, the step whose `down` was missing or failed. Runs without the
   * run's signal, so a canceled run still rolls back.
   */
  private async rollback(d: SchemaDriver, current: string, applied: Migration[], bk: BackupState): Promise<string> {
    for (let i = applied.length - 1; i >= 0; i--) {
      const mig = applied[i];
      let downOk = false;
      if (mig.down) {
        try {
          await mig.down();
          downOk = true;
        } catch {
          downOk = false;
        }
      }
      if (!downOk) {
        if (bk.path === '') throw new NotRestoredError(ERR_NO_BACKUP, { cause: bk.unsupported });
        try {
          await d.restore(bk.path);
        } catch (e) {
          throw isBackupUnsupported(e) ? new NotRestoredError(ERR_RESTORE_UNSUPPORTED, { cause: e }) : e;
        }
        try {
          await this.setVersion(d, current);
        } catch (e) {
          throw recordError(current, e);
        }
        return 'restore';
      }
      const prev = i > 0 ? applied[i - 1].version : current;
      try {
        await this.setVersion(d, prev);
      } catch (e) {
        throw recordError(prev, e);
      }
    }
    return 'down';
  }

  /**
   * Records a version for the driver. A driver with `storesVersion` owns
   * its version: only `setVersion` is called, no version file is touched.
   * Any other driver gets the version file first (the canonical copy) and
   * then `setVersion` as a notification, so a refused `setVersion` leaves
   * the file at the new version.
   */
  private async setVersion(d: SchemaDriver, version: string): Promise<void> {
    if (d.storesVersion !== true) await writeVersionFile(this.baseDir(), d.name(), version);
    await d.setVersion(version);
  }
}
