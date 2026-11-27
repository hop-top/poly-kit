/**
 * A {@link SchemaDriver} for configuration files, ported from Go
 * `core/upgrade/driver/configfile`: backup copies each managed file into
 * the backup directory, restore copies them back. The version lives in
 * the Migrator's version file.
 *
 * @packageDocumentation
 */

import { promises as fs } from 'node:fs';
import * as path from 'node:path';

import { versionOf, type DriverOptions } from './version';

/** Options of a {@link ConfigFileDriver}. */
export interface ConfigFileOptions extends DriverOptions {
  /** The schema name (default `"config"`). */
  name?: string;
}

const isMissing = (e: unknown): boolean => (e as NodeJS.ErrnoException).code === 'ENOENT';
const msg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

/**
 * The backup file name of a managed path: the cleaned path with every
 * separator turned into `_` (`/etc/foo/config.yaml` becomes
 * `_etc_foo_config.yaml`), so two files named alike never collide.
 */
export function backupFileName(p: string): string {
  let clean = path.normalize(p);
  if (clean.length > 1 && clean.endsWith(path.sep)) clean = clean.slice(0, -1);
  return clean.split(path.sep).join('_');
}

/**
 * Backs up and restores one or more configuration files.
 *
 * A `SchemaDriver`; not declared with `implements` so the bundled declarations
 * keep `SchemaDriver` out of a chunk shared with the root barrel (the tests
 * check the fit).
 */
export class ConfigFileDriver {
  private readonly schema: string;
  private readonly paths: readonly string[];
  private readonly opts: DriverOptions;

  constructor(paths: readonly string[], options: ConfigFileOptions = {}) {
    this.paths = [...paths];
    this.schema = options.name ?? 'config';
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

  /** Copies each managed file into `dest`; a file that does not exist is skipped. */
  async backup(dest: string): Promise<void> {
    try {
      await fs.mkdir(dest, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create backup dir: ${msg(e)}`, { cause: e });
    }
    for (const p of this.paths) {
      const name = backupFileName(p);
      try {
        await copyFile(p, path.join(dest, name));
      } catch (e) {
        if (isMissing(e)) continue;
        throw new Error(`backup ${name}: ${msg(e)}`, { cause: e });
      }
    }
  }

  /** Copies each backed-up file back to its place; a file absent from `src` is skipped. */
  async restore(src: string): Promise<void> {
    for (const p of this.paths) {
      const name = backupFileName(p);
      const file = path.join(src, name);
      try {
        await fs.stat(file);
      } catch (e) {
        if (isMissing(e)) continue;
        throw new Error(`restore ${name}: ${msg(e)}`, { cause: e });
      }
      try {
        await copyFile(file, p);
      } catch (e) {
        throw new Error(`restore ${name}: ${msg(e)}`, { cause: e });
      }
    }
  }
}

/** Copies `src` over `dst`, creating `dst`'s directory. An existing `dst` keeps its mode. */
async function copyFile(src: string, dst: string): Promise<void> {
  const data = await fs.readFile(src);
  await fs.mkdir(path.dirname(dst), { recursive: true, mode: 0o750 });
  await fs.writeFile(dst, data);
}
