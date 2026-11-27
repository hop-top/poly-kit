import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { tempDir } from '../../../test/upgrade/driver-support';
import { Migrator, Registry, isNotRestored, readVersionFile, type SchemaDriver } from '../index';
import { ConfigFileDriver, backupFileName } from './configfile';

let tmp: ReturnType<typeof tempDir>;
beforeEach(() => {
  tmp = tempDir();
});
afterEach(() => tmp.cleanup());

describe('ConfigFileDriver', () => {
  it('names the schema "config" unless told otherwise', () => {
    const asDriver: SchemaDriver = new ConfigFileDriver([]); // the fit the class does not declare
    expect(asDriver.name()).toBe('config');
    expect(new ConfigFileDriver([], { name: 'app' }).name()).toBe('app');
  });

  it('backs a file up and restores it', async () => {
    const cfg = join(tmp.dir, 'config.yaml');
    writeFileSync(cfg, 'key: value');
    const d = new ConfigFileDriver([cfg]);
    const backup = join(tmp.dir, 'backup');
    await d.backup(backup);
    expect(readFileSync(join(backup, backupFileName(cfg)), 'utf8')).toBe('key: value');

    writeFileSync(cfg, 'key: changed');
    await d.restore(backup);
    expect(readFileSync(cfg, 'utf8')).toBe('key: value');
  });

  it('keeps same-named files in different directories apart', async () => {
    mkdirSync(join(tmp.dir, 'a'));
    mkdirSync(join(tmp.dir, 'b'));
    const a = join(tmp.dir, 'a', 'config.yaml');
    const b = join(tmp.dir, 'b', 'config.yaml');
    writeFileSync(a, 'a');
    writeFileSync(b, 'b');
    const backup = join(tmp.dir, 'backup');
    await new ConfigFileDriver([a, b]).backup(backup);
    expect(readFileSync(join(backup, backupFileName(a)), 'utf8')).toBe('a');
    expect(readFileSync(join(backup, backupFileName(b)), 'utf8')).toBe('b');
  });

  it('flattens a path like Go: separators become underscores', () => {
    expect(backupFileName('/etc/foo/config.yaml')).toBe('_etc_foo_config.yaml');
    expect(backupFileName('/etc//foo/./config.yaml/')).toBe('_etc_foo_config.yaml');
  });

  it('skips a missing file on backup and a file absent from the backup on restore', async () => {
    const d = new ConfigFileDriver([join(tmp.dir, 'nope.yaml')]);
    const backup = join(tmp.dir, 'backup');
    await d.backup(backup);
    await d.restore(backup);
    expect(existsSync(join(tmp.dir, 'nope.yaml'))).toBe(false);
  });

  it('reads the version file under dataDir', async () => {
    mkdirSync(join(tmp.dir, 'config'));
    writeFileSync(join(tmp.dir, 'config', 'version'), '2.0.0\n');
    expect(await new ConfigFileDriver([], { dataDir: tmp.dir }).version()).toBe('2.0.0');
    expect(await new ConfigFileDriver([], { dataDir: join(tmp.dir, 'none') }).version()).toBe('');
  });
});

describe('through the Migrator', () => {
  it('restores the file when a later migration fails and the earlier one has no down', async () => {
    const data = join(tmp.dir, 'data');
    const cfg = join(tmp.dir, 'config.yaml');
    mkdirSync(join(data, 'config'), { recursive: true });
    writeFileSync(join(data, 'config', 'version'), '1.0.0');
    writeFileSync(cfg, 'v: 1');

    const reg = new Registry();
    reg.register({ version: '1.1.0', schema: 'config', up: () => writeFileSync(cfg, 'v: 2') });
    reg.register({
      version: '1.2.0',
      schema: 'config',
      up: () => {
        throw new Error('simulated failure');
      },
    });
    const m = new Migrator('tool', '1.2.0', { registry: reg, dataDir: data, warn: () => {} });
    m.addDriver(new ConfigFileDriver([cfg], { dataDir: data }));

    const err = await m.run().then(
      () => undefined,
      (e: unknown) => e as Error,
    );
    expect(err?.message).toContain('simulated failure');
    expect(isNotRestored(err)).toBe(false);
    expect(readFileSync(cfg, 'utf8')).toBe('v: 1');
    expect(await readVersionFile(data, 'config')).toBe('1.0.0');
  });

  it('applies every migration and keeps the new content on success', async () => {
    const data = join(tmp.dir, 'data');
    const cfg = join(tmp.dir, 'config.yaml');
    writeFileSync(cfg, 'v: 1');
    const reg = new Registry();
    reg.register({ version: '1.0.0', schema: 'config', up: () => writeFileSync(cfg, 'v: 2') });
    const m = new Migrator('tool', '1.0.0', { registry: reg, dataDir: data });
    m.addDriver(new ConfigFileDriver([cfg], { dataDir: data }));
    await m.run();
    expect(readFileSync(cfg, 'utf8')).toBe('v: 2');
    expect(readFileSync(join(data, 'backups', 'config', 'pre-1.0.0', backupFileName(cfg)), 'utf8')).toBe('v: 1');
  });
});
