import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { execFileSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import * as tar from 'tar';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { tempDir, writeRawTarGz, type RawMember } from '../../../test/upgrade/driver-support';
import { Migrator, Registry, isNotRestored, readVersionFile, type SchemaDriver } from '../index';
import { FsDirDriver, unsafeName } from './fsdir';

const FIXTURE = join(dirname(fileURLToPath(import.meta.url)), '../../../test/upgrade/testdata/go-fsdir-mydata.tar.gz');

let tmp: ReturnType<typeof tempDir>;
let dir: string; // the managed directory
let backup: string;
beforeEach(() => {
  tmp = tempDir();
  dir = join(tmp.dir, 'mydata');
  backup = join(tmp.dir, 'backup');
  mkdirSync(join(dir, 'sub'), { recursive: true });
  writeFileSync(join(dir, 'file.txt'), 'hello');
  writeFileSync(join(dir, 'sub', 'nested.txt'), 'world');
});
afterEach(() => tmp.cleanup());

const names = async (archive: string): Promise<string[]> => {
  const out: string[] = [];
  await tar.t({ file: archive, onReadEntry: (e) => void out.push(e.path.replace(/\/$/, '')) });
  return out.sort();
};

describe('FsDirDriver backup and restore', () => {
  it('fits SchemaDriver', () => {
    const d: SchemaDriver = new FsDirDriver('mydata', dir);
    expect(d.name()).toBe('mydata');
  });

  it('round trips a directory', async () => {
    const d = new FsDirDriver('mydata', dir);
    await d.backup(backup);
    expect(existsSync(join(backup, 'mydata.tar.gz'))).toBe(true);

    writeFileSync(join(dir, 'file.txt'), 'changed');
    writeFileSync(join(dir, 'extra.txt'), 'new');
    await d.restore(backup);

    expect(readFileSync(join(dir, 'file.txt'), 'utf8')).toBe('hello');
    expect(readFileSync(join(dir, 'sub', 'nested.txt'), 'utf8')).toBe('world');
    expect(existsSync(join(dir, 'extra.txt'))).toBe(false);
    expect(readdirSync(tmp.dir).sort()).toEqual(['backup', 'mydata']); // no temp leftovers
  });

  it('backs up nothing for a missing directory', async () => {
    await new FsDirDriver('missing', join(tmp.dir, 'nope')).backup(backup);
    expect(readdirSync(backup)).toEqual([]);
  });

  it('fails a restore without an archive', async () => {
    await expect(new FsDirDriver('mydata', dir).restore(tmp.dir)).rejects.toThrow(/backup archive not found/);
    expect(readFileSync(join(dir, 'file.txt'), 'utf8')).toBe('hello');
  });

  it('restores into a directory that no longer exists', async () => {
    const d = new FsDirDriver('mydata', dir);
    await d.backup(backup);
    execFileSync('rm', ['-rf', dir]);
    await d.restore(backup);
    expect(readFileSync(join(dir, 'sub', 'nested.txt'), 'utf8')).toBe('world');
  });

  it('refuses to back up a symbolic link instead of dropping it', async () => {
    symlinkSync('file.txt', join(dir, 'link'));
    await expect(new FsDirDriver('mydata', dir).backup(backup)).rejects.toThrow(/only files and directories/);
    expect(existsSync(join(backup, 'mydata.tar.gz'))).toBe(false);
  });
});

describe('archive layout shared with Go', () => {
  it('writes Go-style member names: relative, "." for the directory', async () => {
    await new FsDirDriver('mydata', dir).backup(backup);
    expect(await names(join(backup, 'mydata.tar.gz'))).toEqual(['.', 'file.txt', 'sub', 'sub/nested.txt']);
  });

  it('restores an archive written by the Go driver', async () => {
    mkdirSync(backup);
    copyFileSync(FIXTURE, join(backup, 'mydata.tar.gz'));
    writeFileSync(join(dir, 'file.txt'), 'stale');
    await new FsDirDriver('mydata', dir).restore(backup);
    expect(readFileSync(join(dir, 'file.txt'), 'utf8')).toBe('hello');
    expect(readFileSync(join(dir, 'sub', 'nested.txt'), 'utf8')).toBe('world');
    expect(readdirSync(join(dir, 'empty'))).toEqual([]);
  });
});

describe('traversal guard', () => {
  const restoreOf = async (members: RawMember[]): Promise<Error> => {
    mkdirSync(backup, { recursive: true });
    writeRawTarGz(join(backup, 'mydata.tar.gz'), members);
    const err = await new FsDirDriver('mydata', dir).restore(backup).then(
      () => undefined,
      (e: unknown) => e as Error,
    );
    expect(err, 'restore should have been refused').toBeDefined();
    // Nothing escaped, and the directory is as it was.
    expect(existsSync(join(tmp.dir, 'evil.txt'))).toBe(false);
    expect(readFileSync(join(dir, 'file.txt'), 'utf8')).toBe('hello');
    expect(readdirSync(tmp.dir).sort()).toEqual(['backup', 'mydata']);
    return err as Error;
  };

  it('rejects a ".." member', async () => {
    const err = await restoreOf([{ path: 'ok.txt', body: 'x' }, { path: '../evil.txt', body: 'x' }]);
    expect(err.message).toBe('path traversal blocked: ../evil.txt');
  });

  it('rejects ".." in the middle of a name', async () => {
    const err = await restoreOf([{ path: 'a/../../evil.txt', body: 'x' }]);
    expect(err.message).toMatch(/^path traversal blocked: /);
  });

  it('rejects an absolute name', async () => {
    const err = await restoreOf([{ path: join(tmp.dir, 'evil.txt'), body: 'x' }]);
    expect(err.message).toMatch(/^path traversal blocked: /);
  });

  it('rejects a symbolic link member, escaping or not', async () => {
    const err = await restoreOf([{ path: 'link', type: 'SymbolicLink', linkpath: '../../etc' }]);
    expect(err.message).toMatch(/^special member blocked: link \(SymbolicLink\)/);
    const inside = await restoreOf([{ path: 'link2', type: 'SymbolicLink', linkpath: 'file.txt' }]);
    expect(inside.message).toMatch(/special member blocked: link2/);
  });

  it('rejects a hard link member', async () => {
    const err = await restoreOf([{ path: 'h', type: 'Link', linkpath: '/etc/passwd' }]);
    expect(err.message).toMatch(/^special member blocked: h \(Link\)/);
  });

  it('rejects a device or fifo member', async () => {
    const err = await restoreOf([{ path: 'dev', type: 'CharacterDevice' }]);
    expect(err.message).toMatch(/^special member blocked: dev/);
    const fifo = await restoreOf([{ path: 'pipe', type: 'FIFO' }]);
    expect(fifo.message).toMatch(/^special member blocked: pipe/);
  });

  it('unsafeName accepts plain relative names only', () => {
    for (const ok of ['.', 'a', 'a/b.txt', 'sub/', '..foo', 'a..b/c']) expect(unsafeName(ok), ok).toBeUndefined();
    for (const bad of ['..', '../x', 'a/../b', '/x', '\\x', 'C:x', 'a\\..\\b', 'a\0b']) {
      expect(unsafeName(bad), bad).toBeDefined();
    }
  });
});

describe('through the Migrator', () => {
  it('restores the directory when a migration fails and nothing can undo it', async () => {
    const data = join(tmp.dir, 'data');
    mkdirSync(join(data, 'mydata'), { recursive: true });
    writeFileSync(join(data, 'mydata', 'version'), '1.0.0');
    const reg = new Registry();
    reg.register({ version: '1.1.0', schema: 'mydata', up: () => writeFileSync(join(dir, 'file.txt'), 'migrated') });
    reg.register({
      version: '1.2.0',
      schema: 'mydata',
      up: () => {
        throw new Error('simulated failure');
      },
    });
    const m = new Migrator('tool', '1.2.0', { registry: reg, dataDir: data, warn: () => {} });
    m.addDriver(new FsDirDriver('mydata', dir, { dataDir: data }));
    const err = await m.run().then(
      () => undefined,
      (e: unknown) => e as Error,
    );
    expect(err?.message).toContain('simulated failure');
    expect(isNotRestored(err)).toBe(false);
    expect(readFileSync(join(dir, 'file.txt'), 'utf8')).toBe('hello');
    expect(await readVersionFile(data, 'mydata')).toBe('1.0.0');
  });
});
