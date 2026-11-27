// migrator.json: the scenario types, their strict shape and the runner,
// modelled on the Go runner (recordingDriver, storingDriver, lockingDriver).

import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync, chmodSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';

import { expect } from 'vitest';

import type { Event } from '../../src/bus';
import type { Shape } from './conformance-support';
import {
  BackupUnsupportedError,
  Migrator,
  Registry,
  abortError,
  defaultRegistry,
  readVersionFile,
  registerMigration,
  resetRegistryForTest,
  type Migration,
  type MigratorOptions,
  type SchemaDriver,
} from '../../src/upgrade/index';

const TOOL = 'conftool';

interface DriverCase {
  name: string;
  version?: string;
  backup_fail?: string;
  restore_fail?: string;
  backup_unsupported?: boolean;
  restore_unsupported?: boolean;
  version_fail?: string;
  set_version_fail?: string;
  set_version_fail_for?: string;
  stores_version?: boolean;
  lock?: boolean;
  lock_fail?: string;
  lock_blocked?: boolean;
  lock_sets_version?: string;
  unlock_fail?: string;
}

interface MigrationCase {
  schema: string;
  version: string;
  fail?: string;
  down?: boolean;
  down_fail?: string;
  verify?: boolean;
  verify_fail?: string;
  cancel?: boolean;
}

interface EventCase {
  topic: string;
  payload: Record<string, unknown>;
}

interface Step {
  op: string;
  canceled?: boolean;
  events?: EventCase[];
  warnings?: string[];
  error?: string | null;
  calls?: string[];
  history?: { schema: string; version: string }[];
  versions?: Record<string, string>;
  backups?: Record<string, string[]>;
  stored?: Record<string, string>;
  pre_run_versions?: Record<string, Record<string, string>>;
  no_version_dir?: string[];
  status?: { schema: string; current: string; target: string; pending: number; error: string | null }[];
}

export interface Scenario {
  name: string;
  target: string;
  manual_rollback?: boolean;
  retention?: number;
  drivers: DriverCase[];
  migrations: MigrationCase[];
  backups?: Record<string, string[]>;
  recorded_backups?: Record<string, Record<string, string>>;
  locked_backups?: Record<string, string[]>;
  baseline?: Record<string, { version?: string; error?: string }>;
  final_verify?: boolean;
  final_verify_fail?: string;
  data_dir?: boolean;
  registry?: string;
  default_migrations?: MigrationCase[];
  steps: Step[];
}

export interface MigratorFile {
  scenarios: Scenario[];
}

const MIGRATION_SHAPE: Shape = {
  schema: true,
  version: true,
  fail: true,
  down: true,
  down_fail: true,
  verify: true,
  verify_fail: true,
  cancel: true,
};

export const MIGRATOR_SHAPE: Shape = {
  scenarios: [
    {
      name: true,
      target: true,
      manual_rollback: true,
      retention: true,
      drivers: [
        {
          name: true,
          version: true,
          backup_fail: true,
          restore_fail: true,
          backup_unsupported: true,
          restore_unsupported: true,
          version_fail: true,
          set_version_fail: true,
          set_version_fail_for: true,
          stores_version: true,
          lock: true,
          lock_fail: true,
          lock_blocked: true,
          lock_sets_version: true,
          unlock_fail: true,
        },
      ],
      migrations: [MIGRATION_SHAPE],
      backups: true,
      recorded_backups: true,
      locked_backups: true,
      baseline: true,
      final_verify: true,
      final_verify_fail: true,
      data_dir: true,
      registry: true,
      default_migrations: [MIGRATION_SHAPE],
      steps: [
        {
          op: true,
          canceled: true,
          events: [{ topic: true, payload: { tool: true, to: true, applied: true, schema: true, error: true, from: true, stage: true, method: true, final: true } }],
          warnings: [true],
          error: true,
          calls: [true],
          history: [{ schema: true, version: true }],
          versions: true,
          backups: true,
          stored: true,
          pre_run_versions: true,
          no_version_dir: [true],
          status: [{ schema: true, current: true, target: true, pending: true, error: true }],
        },
      ],
    },
  ],
};

const failIf = (msg: string | undefined): void => {
  if (msg) throw new Error(msg);
};

/** A recording driver: every state-changing call goes to `record`. */
function recordingDriver(dc: DriverCase, record: (e: string) => void): SchemaDriver {
  let version = dc.version ?? '';
  const name = dc.name;
  const d: SchemaDriver & { lock?: SchemaDriver['lock']; storesVersion?: boolean } = {
    name: () => name,
    version() {
      if (dc.lock) record(name + ' version');
      if (dc.version_fail) throw new Error(dc.version_fail);
      return version;
    },
    setVersion(v) {
      record(`${name} set_version ${v}`);
      if (dc.set_version_fail && (!dc.set_version_fail_for || dc.set_version_fail_for === v)) {
        throw new Error(dc.set_version_fail);
      }
      version = v;
    },
    backup(dest) {
      record(`${name} backup ${basename(dest)}`);
      if (dc.backup_unsupported) throw new BackupUnsupportedError();
      failIf(dc.backup_fail);
    },
    restore(src) {
      record(`${name} restore ${basename(src)}`);
      if (dc.restore_unsupported) throw new BackupUnsupportedError();
      failIf(dc.restore_fail);
    },
  };
  if (dc.stores_version) d.storesVersion = true;
  if (dc.lock) {
    d.lock = (signal) => {
      record(name + ' lock');
      if (dc.lock_blocked) {
        if (signal?.aborted) throw abortError(signal);
        throw new Error('lock would block forever: the step must set canceled');
      }
      failIf(dc.lock_fail);
      if (dc.lock_sets_version) version = dc.lock_sets_version;
      return () => {
        record(name + ' unlock');
        failIf(dc.unlock_fail);
      };
    };
  }
  return d;
}

const listDirs = (base: string, schema: string): string[] => {
  try {
    return readdirSync(join(base, 'backups', schema), { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name)
      .sort();
  } catch {
    return [];
  }
};

const listPreRun = (base: string, schema: string): Record<string, string> => {
  const dir = join(base, 'backups', schema);
  const out: Record<string, string> = {};
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const e of entries) {
    if (e.isDirectory() || !e.name.endsWith('.version')) continue;
    out[e.name.slice(0, -'.version'.length)] = readFileSync(join(dir, e.name), 'utf8');
  }
  return out;
};

const exists = (p: string): boolean => {
  try {
    statSync(p);
    return true;
  } catch {
    return false;
  }
};

export async function runMigratorScenario(sc: Scenario): Promise<void> {
  const tmp = mkdtempSync(join(tmpdir(), 'upgrade-conf-'));
  const savedXdg = process.env['XDG_DATA_HOME'];
  const cleanups: (() => void)[] = [];
  resetRegistryForTest();
  try {
    process.env['XDG_DATA_HOME'] = join(tmp, 'xdg');
    const xdgBase = join(tmp, 'xdg', 'hop', TOOL);
    const base = sc.data_dir ? join(tmp, 'custom') : xdgBase;

    const mkBackups = (m: Record<string, string[]> | undefined, locked: boolean): void => {
      for (const [schema, dirs] of Object.entries(m ?? {})) {
        mkdirSync(join(base, 'backups', schema), { recursive: true, mode: 0o750 });
        for (const d of dirs) {
          const dir = join(base, 'backups', schema, d);
          if (!locked) {
            mkdirSync(dir, { recursive: true, mode: 0o750 });
            continue;
          }
          const inner = join(dir, 'locked');
          mkdirSync(inner, { recursive: true, mode: 0o750 });
          writeFileSync(join(inner, 'f'), '', { mode: 0o600 });
          chmodSync(inner, 0o500);
          cleanups.push(() => chmodSync(inner, 0o750));
        }
      }
    };
    for (const [schema, recorded] of Object.entries(sc.recorded_backups ?? {})) {
      const dir = join(base, 'backups', schema);
      mkdirSync(dir, { recursive: true, mode: 0o750 });
      for (const [name, pre] of Object.entries(recorded)) {
        mkdirSync(join(dir, name), { recursive: true, mode: 0o750 });
        writeFileSync(join(dir, name + '.version'), pre, { mode: 0o600 });
      }
    }
    mkBackups(sc.backups, false);
    mkBackups(sc.locked_backups, true);

    let calls: string[] = [];
    const record = (e: string): void => void calls.push(e);
    const ctl = new AbortController();

    const build = (mc: MigrationCase): Migration => {
      const mig: Migration = {
        version: mc.version,
        schema: mc.schema,
        up() {
          record(`${mc.schema} up ${mc.version}`);
          if (mc.cancel) ctl.abort();
          failIf(mc.fail);
        },
      };
      if (mc.down || mc.down_fail) {
        mig.down = () => {
          record(`${mc.schema} down ${mc.version}`);
          failIf(mc.down_fail);
        };
      }
      if (mc.verify || mc.verify_fail) {
        mig.verify = () => {
          record(`${mc.schema} verify ${mc.version}`);
          failIf(mc.verify_fail);
        };
      }
      return mig;
    };

    let reg = defaultRegistry();
    const opts: MigratorOptions = {};
    if (sc.registry === 'own') {
      reg = new Registry();
      opts.registry = reg;
      for (const mc of sc.default_migrations ?? []) registerMigration(build(mc));
    }
    for (const mc of sc.migrations) reg.register(build(mc));
    if (sc.manual_rollback) opts.manualRollback = true;
    if (sc.retention) opts.backupRetention = sc.retention;
    if (sc.data_dir) opts.dataDir = base;
    if (sc.final_verify || sc.final_verify_fail) {
      opts.verify = (schema, version) => {
        record(`${schema} final_verify ${version}`);
        failIf(sc.final_verify_fail);
      };
    }
    if (sc.baseline) {
      const baseline = sc.baseline;
      opts.baseline = (schema) => {
        record(schema + ' baseline');
        const b = baseline[schema] ?? {};
        failIf(b.error);
        return b.version ?? '';
      };
    }
    let events: EventCase[] = [];
    let warnings: string[] = [];
    opts.publisher = {
      publish(e: Event) {
        expect(e.source).toBe('core.upgrade');
        const payload = { ...(e.payload as Record<string, unknown>) };
        if ('duration_ms' in payload) {
          const d = payload['duration_ms'];
          expect(Number.isInteger(d) && (d as number) >= 0, `${e.topic}: duration_ms = ${String(d)}`).toBe(true);
          delete payload['duration_ms'];
        }
        events.push({ topic: e.topic, payload });
      },
    };
    opts.warn = (m) => void warnings.push(m);

    const m = new Migrator(TOOL, sc.target, opts);
    for (const dc of sc.drivers) m.addDriver(recordingDriver(dc, record));

    for (const [i, st] of sc.steps.entries()) {
      const at = `step ${i}`;
      calls = [];
      events = [];
      warnings = [];
      let signal = ctl.signal;
      if (st.canceled) {
        const c = new AbortController();
        c.abort();
        signal = c.signal;
      }
      let err: unknown;
      try {
        if (st.op === 'run') await m.run(signal);
        else if (st.op === 'rollback_latest') await m.rollbackLatest();
        else if (st.op === 'status') {
          const got = (await m.status()).map((s) => ({
            schema: s.schema,
            current: s.current,
            target: s.target,
            pending: s.pending,
            error: s.error ? s.error.message : null,
          }));
          expect(st.status, `${at}: status op without status`).toBeDefined();
          expect(got, at).toEqual(st.status);
        } else throw new Error(`${at}: unknown op ${st.op}`);
      } catch (e) {
        err = e;
      }

      if (st.error === null || st.error === undefined) {
        expect(err, `${at}: unexpected error ${String((err as Error)?.message)}`).toBeUndefined();
      } else {
        expect(err, `${at}: no error, want ${st.error}`).toBeDefined();
        expect((err as Error).message, at).toBe(st.error);
      }
      if (st.calls) expect(calls, `${at}: calls`).toEqual(st.calls);
      if (st.history) expect(m.history(), `${at}: history`).toEqual(st.history);
      if (st.events) expect(events, `${at}: events`).toEqual(st.events);
      if (st.warnings) {
        expect(warnings.length, `${at}: logged ${JSON.stringify(warnings)}, want prefixes ${JSON.stringify(st.warnings)}`).toBe(st.warnings.length);
        st.warnings.forEach((w, j) => expect(warnings[j].startsWith(w), `${at}: logged ${warnings[j]}, want prefix ${w}`).toBe(true));
      }
      for (const [schema, want] of Object.entries(st.versions ?? {})) {
        expect(await readVersionFile(base, schema), `${at}: ${schema} version file`).toBe(want);
      }
      for (const [schema, want] of Object.entries(st.backups ?? {})) {
        expect(listDirs(base, schema), `${at}: ${schema} backups`).toEqual(want);
      }
      if (st.stored) {
        const got = new Map((await m.status()).map((s) => [s.schema, s.current]));
        for (const [schema, want] of Object.entries(st.stored)) {
          expect(got.get(schema), `${at}: ${schema} stored version`).toBe(want);
        }
      }
      for (const [schema, want] of Object.entries(st.pre_run_versions ?? {})) {
        expect(listPreRun(base, schema), `${at}: ${schema} pre-run versions`).toEqual(want);
      }
      for (const schema of st.no_version_dir ?? []) {
        expect(exists(join(base, schema)), `${at}: ${schema} has a version dir`).toBe(false);
      }
      if (sc.data_dir) expect(exists(xdgBase), `${at}: XDG data dir touched despite dataDir`).toBe(false);
    }
  } finally {
    for (const c of cleanups) c();
    if (savedXdg === undefined) delete process.env['XDG_DATA_HOME'];
    else process.env['XDG_DATA_HOME'] = savedXdg;
    resetRegistryForTest();
    rmSync(tmp, { recursive: true, force: true });
  }
}
