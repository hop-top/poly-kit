/**
 * Backup bookkeeping and the version file, ported from Go
 * `core/upgrade/backup.go`. The layout is Go's, so two ports see the same
 * directory under the same data dir:
 *
 *   <data dir>/<schema>/version               the version file
 *   <data dir>/backups/<schema>/pre-<v>/      a backup, filled by the driver
 *   <data dir>/backups/<schema>/pre-<v>.version   the version the run started at
 *
 * The Migrator writes the version file (for drivers without
 * `storesVersion`) and the `.version` sidecar; drivers fill the backup
 * directory and read the version file back with {@link readVersionFile}.
 */

import { promises as fs } from 'node:fs';
import * as path from 'node:path';

import { dataDir as xdgDataDir } from '../xdg';
import { NotRestoredError, BackupUnsupportedError } from './errors';
import { compareSemver, parseSemver, trimV } from './semver';

export const DEFAULT_RETENTION = 5;

/** Suffix of the file beside a backup directory: `pre-<v>.version`. */
const PRE_RUN_SUFFIX = '.version';

/** The default data dir of a tool: `<xdg data dir>/hop/<tool>`. */
export function defaultDataDir(tool: string): string {
  return xdgDataDir(path.join('hop', tool));
}

/**
 * Reads `<dataDir>/<schema>/version`, trimmed. Resolves `""` when the file
 * does not exist. For drivers that read what the Migrator recorded.
 */
export async function readVersionFile(dataDir: string, schema: string): Promise<string> {
  try {
    return (await fs.readFile(path.join(dataDir, schema, 'version'), 'utf8')).trim();
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === 'ENOENT') return '';
    throw e;
  }
}

/** Writes `<dataDir>/<schema>/version`, creating the directory. */
export async function writeVersionFile(dataDir: string, schema: string, version: string): Promise<void> {
  const dir = path.join(dataDir, schema);
  try {
    await fs.mkdir(dir, { recursive: true, mode: 0o750 });
  } catch (e) {
    throw new Error(`create version dir: ${(e as Error).message}`, { cause: e });
  }
  try {
    await fs.writeFile(path.join(dir, 'version'), version, { mode: 0o600 });
  } catch (e) {
    throw new Error(`write version file: ${(e as Error).message}`, { cause: e });
  }
}

/** The versioned `pre-` directories of a schema, newest semver first. */
async function backupDirs(schemaDir: string): Promise<{ name: string; v: ReturnType<typeof parseSemver> }[]> {
  const entries = await fs.readdir(schemaDir, { withFileTypes: true });
  const dirs: { name: string; v: ReturnType<typeof parseSemver> }[] = [];
  for (const e of entries) {
    if (!e.isDirectory() || !e.name.startsWith('pre-')) continue;
    dirs.push({ name: e.name, v: parseSemver(trimV(e.name.slice('pre-'.length))) });
  }
  return dirs.sort((a, b) => compareSemver(b.v, a.v));
}

const isMissing = (e: unknown): boolean => (e as NodeJS.ErrnoException).code === 'ENOENT';

/** Backup lifecycle of one schema. */
export class BackupStore {
  private readonly retention: number;

  constructor(
    private readonly schema: string,
    private readonly base: string,
    retention: number,
  ) {
    this.retention = retention <= 0 ? DEFAULT_RETENTION : retention;
  }

  private get schemaDir(): string {
    return path.join(this.base, 'backups', this.schema);
  }

  /** Creates and returns the directory of the backup taken before `version`. */
  async backupDir(version: string): Promise<string> {
    const dir = path.join(this.schemaDir, 'pre-' + version);
    try {
      await fs.mkdir(dir, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create backup dir: ${(e as Error).message}`, { cause: e });
    }
    return dir;
  }

  async hasBackup(version: string): Promise<boolean> {
    try {
      await fs.stat(path.join(this.schemaDir, 'pre-' + version));
      return true;
    } catch {
      return false;
    }
  }

  /** Removes a backup directory (after an unsupported backup). */
  async discard(backupPath: string): Promise<void> {
    await fs.rm(backupPath, { recursive: true, force: true });
  }

  /** Saves, beside the backup, the version the run started at ("" is an empty file). */
  async savePreRun(backupPath: string, version: string): Promise<void> {
    try {
      await fs.writeFile(backupPath + PRE_RUN_SUFFIX, version, { mode: 0o600 });
    } catch (e) {
      throw new Error(`write pre-run version: ${(e as Error).message}`, { cause: e });
    }
  }

  /** The version saved beside `backupPath`; `recorded` is false for a backup taken before it was saved. */
  async readPreRun(backupPath: string): Promise<{ version: string; recorded: boolean }> {
    try {
      const data = await fs.readFile(backupPath + PRE_RUN_SUFFIX, 'utf8');
      return { version: data.trim(), recorded: true };
    } catch (e) {
      if (isMissing(e)) return { version: '', recorded: false };
      throw new Error(`read pre-run version: ${(e as Error).message}`, { cause: e });
    }
  }

  /** The most recent backup directory by semver. */
  async latestBackup(): Promise<string> {
    let dirs: Awaited<ReturnType<typeof backupDirs>>;
    try {
      dirs = await backupDirs(this.schemaDir);
    } catch (e) {
      if (isMissing(e)) throw this.noBackups();
      throw new Error(`read backup dir: ${(e as Error).message}`, { cause: e });
    }
    if (dirs.length === 0) throw this.noBackups();
    return path.join(this.schemaDir, dirs[0].name);
  }

  /** No backup at all: not restored, with unsupported as its cause. */
  private noBackups(): NotRestoredError {
    return new NotRestoredError('no backups found for ' + this.schema, {
      cause: new BackupUnsupportedError(),
    });
  }

  /** Keeps the `retention` most recent backups by semver, removing the rest and their sidecars. */
  async prune(): Promise<void> {
    let dirs: Awaited<ReturnType<typeof backupDirs>>;
    try {
      dirs = await backupDirs(this.schemaDir);
    } catch (e) {
      if (isMissing(e)) return;
      throw new Error(`read backup dir: ${(e as Error).message}`, { cause: e });
    }
    // Oldest first, like Go: the first failure stops the prune.
    for (const d of dirs.slice(this.retention).reverse()) {
      const p = path.join(this.schemaDir, d.name);
      try {
        await fs.rm(p, { recursive: true, force: true });
        await fs.rm(p + PRE_RUN_SUFFIX, { force: true });
      } catch (e) {
        throw new Error(`prune backup ${d.name}: ${(e as Error).message}`, { cause: e });
      }
    }
  }
}
