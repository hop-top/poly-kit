/**
 * A {@link SchemaDriver} for a filesystem directory, ported from Go
 * `core/upgrade/driver/fsdir`: backup writes `<name>.tar.gz` into the
 * backup directory, restore replaces the directory with its contents.
 * The archive layout is Go's (entries relative to the directory, `.` for
 * the directory itself, gzip), so either port restores the other's.
 *
 * Imports the `tar` package: import this module only where it is used
 * (`@hop-top/kit/upgrade/drivers/fsdir`); the main entry never loads it.
 *
 * @packageDocumentation
 */

import { promises as fs } from 'node:fs';
import * as path from 'node:path';
import { randomBytes } from 'node:crypto';

import * as tar from 'tar';

import { versionOf, type DriverOptions } from './version';

/** Options of an {@link FsDirDriver}. */
export type FsDirOptions = DriverOptions;

const isMissing = (e: unknown): boolean => (e as NodeJS.ErrnoException).code === 'ENOENT';
const msg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

/** Member types a backup holds and a restore accepts: files and directories only. */
const PLAIN = new Set(['File', 'OldFile', 'ContiguousFile', 'Directory']);

/**
 * Why a member name must not be extracted under the target directory, or
 * `undefined` when it is a plain relative path inside it. Rejects absolute
 * names (POSIX, backslash and drive-letter forms), any `..` segment and
 * NUL bytes, on top of what the `tar` package does by default (it strips
 * a leading `/` and skips `..` members with a warning instead of failing).
 */
export function unsafeName(name: string): string | undefined {
  if (name.includes('\0')) return 'NUL in name';
  if (name.startsWith('/') || name.startsWith('\\') || /^[A-Za-z]:/.test(name)) return 'absolute path';
  if (name.split(/[\\/]/).includes('..')) return 'parent directory reference';
  return undefined;
}

/**
 * Backs up and restores one directory as a tar.gz archive.
 *
 * A `SchemaDriver`; not declared with `implements` so the bundled declarations
 * keep `SchemaDriver` out of a chunk shared with the root barrel (the tests
 * check the fit).
 */
export class FsDirDriver {
  private readonly opts: DriverOptions;

  constructor(
    private readonly schema: string,
    private readonly dirPath: string,
    options: FsDirOptions = {},
  ) {
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

  private archiveName(): string {
    return this.schema + '.tar.gz';
  }

  /**
   * Writes `<dest>/<name>.tar.gz` of the directory; a missing directory is
   * "nothing to back up" (no archive, no error). Symbolic links and special
   * files are not archived: they make the backup fail rather than be lost.
   */
  async backup(dest: string): Promise<void> {
    try {
      await fs.mkdir(dest, { recursive: true, mode: 0o750 });
    } catch (e) {
      throw new Error(`create backup dir: ${msg(e)}`, { cause: e });
    }
    try {
      await fs.stat(this.dirPath);
    } catch (e) {
      if (isMissing(e)) return;
      throw new Error(`stat dir: ${msg(e)}`, { cause: e });
    }
    await requirePlain(this.dirPath);

    const archive = path.join(dest, this.archiveName());
    const tmp = archive + '.tmp';
    try {
      await tar.c(
        {
          file: tmp,
          cwd: this.dirPath,
          gzip: true,
          // Go's names: relative to the directory, `.` for the directory.
          onWriteEntry: (entry) => {
            entry.path = entry.path.replace(/^\.\//, '') || '.';
          },
        },
        ['.'],
      );
      await fs.rename(tmp, archive);
    } catch (e) {
      await fs.rm(tmp, { force: true });
      throw new Error(`create archive: ${msg(e)}`, { cause: e });
    }
  }

  /**
   * Replaces the directory with the archive in `src`. Every member is
   * checked first (see {@link unsafeName}; links, devices and other
   * special members are refused: `path traversal blocked: <name>`,
   * `special member blocked: <name>`); the
   * archive is then extracted into a temporary directory beside the target
   * and swapped in, so a refused or failed restore leaves the directory
   * as it was.
   */
  async restore(src: string): Promise<void> {
    const archive = path.join(src, this.archiveName());
    try {
      await fs.stat(archive);
    } catch (e) {
      if (isMissing(e)) throw new Error(`backup archive not found: ${archive}`, { cause: e });
      throw new Error(`open archive: ${msg(e)}`, { cause: e });
    }

    const hasRoot = await checkMembers(archive);

    const target = path.resolve(this.dirPath);
    const parent = path.dirname(target);
    const base = path.basename(target);
    await fs.mkdir(parent, { recursive: true, mode: 0o750 });
    const tmp = await fs.mkdtemp(path.join(parent, `.${base}.restore-`));
    let old: string | undefined;
    try {
      try {
        await tar.x({
          file: archive,
          cwd: tmp,
          strict: true,
          preservePaths: false,
          preserveOwner: false,
          filter: (_p, entry) => PLAIN.has((entry as tar.ReadEntry).type),
        });
      } catch (e) {
        throw new Error(`tar read: ${msg(e)}`, { cause: e });
      }
      if (!hasRoot) await fs.chmod(tmp, 0o750);

      try {
        old = path.join(parent, `.${base}.old-${randomBytes(6).toString('hex')}`);
        await fs.rename(target, old);
      } catch (e) {
        if (!isMissing(e)) throw new Error(`remove dir: ${msg(e)}`, { cause: e });
        old = undefined;
      }
      try {
        await fs.rename(tmp, target);
      } catch (e) {
        if (old !== undefined) await fs.rename(old, target).catch(() => undefined);
        throw new Error(`recreate dir: ${msg(e)}`, { cause: e });
      }
    } finally {
      await fs.rm(tmp, { recursive: true, force: true });
    }
    if (old !== undefined) await fs.rm(old, { recursive: true, force: true });
  }
}

/** Throws when the tree holds anything but files and directories. */
async function requirePlain(dir: string): Promise<void> {
  const entries = await fs.readdir(dir, { withFileTypes: true, recursive: true });
  for (const e of entries) {
    if (e.isFile() || e.isDirectory()) continue;
    const rel = path.relative(dir, path.join(e.parentPath, e.name));
    throw new Error(`create archive: ${rel}: only files and directories can be backed up`);
  }
}

/**
 * Reads the archive's headers and throws on the first member that must not
 * be extracted. Resolves whether the archive has an entry for the
 * directory itself (`.`).
 */
async function checkMembers(archive: string): Promise<boolean> {
  let problem: string | undefined;
  let hasRoot = false;
  try {
    await tar.t({
      file: archive,
      strict: true,
      onReadEntry: (entry) => {
        entry.resume();
        if (problem !== undefined) return;
        const bad = unsafeName(entry.path);
        if (bad !== undefined) problem = `path traversal blocked: ${entry.path}`;
        else if (!PLAIN.has(entry.type)) problem = `special member blocked: ${entry.path} (${entry.type})`;
        else if (path.posix.normalize(entry.path) === './' || path.posix.normalize(entry.path) === '.') hasRoot = true;
      },
    });
  } catch (e) {
    throw new Error(`tar read: ${msg(e)}`, { cause: e });
  }
  if (problem !== undefined) throw new Error(problem);
  return hasRoot;
}
