import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { createBus } from '../bus';
import {
  BackupUnsupportedError,
  Migrator,
  Registry,
  abortError,
  isBackupUnsupported,
  isCanceled,
  isNotRestored,
  isVerifyError,
  readVersionFile,
  type SchemaDriver,
  type Unlock,
} from './index';

let dir: string;
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'migrator-test-'));
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

const tick = (ms = 5) => new Promise<void>((r) => setTimeout(r, ms));

/** An async, lockable, version-storing driver over one shared store. */
class SharedStore {
  version = '';
  log: string[] = [];
  private held = false;
  private waiters: (() => void)[] = [];

  async acquire(signal?: AbortSignal): Promise<Unlock> {
    while (this.held) {
      await new Promise<void>((resolve, reject) => {
        const onAbort = () => reject(abortError(signal as AbortSignal));
        if (signal?.aborted) return onAbort();
        signal?.addEventListener('abort', onAbort, { once: true });
        this.waiters.push(() => {
          signal?.removeEventListener('abort', onAbort);
          resolve();
        });
      });
    }
    this.held = true;
    return () => {
      this.held = false;
      this.log.push('unlock');
      this.waiters.shift()?.();
    };
  }

  driver(name = 'db'): SchemaDriver {
    return {
      name: () => name,
      storesVersion: true,
      version: async () => {
        await tick(1);
        return this.version;
      },
      setVersion: async (v) => {
        await tick(1);
        this.version = v;
      },
      backup: () => {
        throw new BackupUnsupportedError();
      },
      restore: () => {
        throw new BackupUnsupportedError();
      },
      lock: (signal) => this.acquire(signal),
    };
  }
}

function registryWith(upLog: string[], ...versions: string[]): Registry {
  const reg = new Registry();
  for (const v of versions) {
    reg.register({
      schema: 'db',
      version: v,
      up: async () => {
        upLog.push('up ' + v);
        await tick(20);
      },
    });
  }
  return reg;
}

describe('Migrator concurrency', () => {
  it('a second run awaits the first unlock, then finds nothing pending', async () => {
    const store = new SharedStore();
    const ups: string[] = [];
    const reg = registryWith(ups, '1.1.0', '1.2.0');
    const a = new Migrator('t', '1.2.0', { registry: reg, dataDir: dir });
    const b = new Migrator('t', '1.2.0', { registry: reg, dataDir: dir });
    a.addDriver(store.driver());
    b.addDriver(store.driver());

    let bDone = false;
    const first = a.run();
    await tick(2); // a holds the lock now
    const second = b.run().then(() => {
      bDone = true;
    });
    await tick(10);
    expect(bDone, 'b must still wait while a holds the lock').toBe(false);
    await first;
    await second;

    expect(ups).toEqual(['up 1.1.0', 'up 1.2.0']);
    expect(a.history()).toHaveLength(2);
    expect(b.history()).toHaveLength(0);
    expect(store.version).toBe('1.2.0');
    expect(store.log).toEqual(['unlock', 'unlock']);
  });

  it('a waiting lock fails with the signal text when the run is aborted', async () => {
    const store = new SharedStore();
    const ups: string[] = [];
    const reg = registryWith(ups, '1.1.0');
    const a = new Migrator('t', '1.1.0', { registry: reg, dataDir: dir });
    const b = new Migrator('t', '1.1.0', { registry: reg, dataDir: dir });
    a.addDriver(store.driver());
    b.addDriver(store.driver());
    const first = a.run();
    await tick(2);
    const ctl = new AbortController();
    const second = b.run(ctl.signal);
    await tick(2);
    ctl.abort();
    const err = await second.then(
      () => undefined,
      (e: unknown) => e,
    );
    expect((err as Error).message).toBe('migrate db: lock: context canceled');
    expect(isCanceled(err)).toBe(true);
    await first;
    expect(store.log).toEqual(['unlock']);
  });
});

describe('Migrator error paths', () => {
  function lockable(log: string[]): SchemaDriver {
    let version = '1.0.0';
    return {
      name: () => 'db',
      version: () => version,
      setVersion: (v) => {
        version = v;
      },
      backup: () => {},
      restore: () => {},
      lock: () => {
        log.push('lock');
        return () => {
          log.push('unlock');
        };
      },
    };
  }

  for (const thrown of ['boom', { code: 7 }, null, 42, undefined]) {
    it(`releases the lock when a migration throws ${JSON.stringify(thrown) ?? 'undefined'}`, async () => {
      const log: string[] = [];
      const reg = new Registry();
      reg.register({
        schema: 'db',
        version: '1.1.0',
        up: () => {
          throw thrown;
        },
      });
      const m = new Migrator('t', '1.1.0', { registry: reg, dataDir: dir, manualRollback: true });
      m.addDriver(lockable(log));
      const err = await m.run().then(
        () => undefined,
        (e: unknown) => e as Error,
      );
      expect(err?.message).toBe(`migrate db: up 1.1.0 failed: ${String(thrown)}`);
      expect(log).toEqual(['lock', 'unlock']);
    });
  }

  it('type guards see not restored, unsupported and verify through the run error', async () => {
    const reg = new Registry();
    reg.register({ schema: 'db', version: '1.1.0', up: () => {}, verify: () => { throw new Error('nope'); } });
    const unsupported: SchemaDriver = {
      name: () => 'db',
      version: () => '1.0.0',
      setVersion: () => {},
      backup: () => {
        throw new BackupUnsupportedError();
      },
      restore: () => {},
    };
    const m = new Migrator('t', '1.1.0', { registry: reg, dataDir: dir });
    m.addDriver(unsupported);
    const err = await m.run().then(
      () => undefined,
      (e: unknown) => e,
    );
    expect((err as Error).message).toBe(
      'migrate db: verify 1.1.0 failed: nope (rollback also failed: not restored: no backup was taken)',
    );
    expect(isNotRestored(err)).toBe(true);
    expect(isBackupUnsupported(err)).toBe(true);
    expect(isVerifyError(err)).toBe(true);
    expect(isCanceled(err)).toBe(false);
  });

  it('an aborted timeout signal reads as a deadline', async () => {
    const reg = new Registry();
    reg.register({ schema: 'db', version: '1.1.0', up: () => {} });
    const m = new Migrator('t', '1.1.0', { registry: reg, dataDir: dir });
    m.addDriver(lockable([]));
    const ctl = new AbortController();
    ctl.abort(new DOMException('slow', 'TimeoutError'));
    const err = await m.run(ctl.signal).then(
      () => undefined,
      (e: unknown) => e as Error,
    );
    expect(err?.message).toBe('migrate db: up 1.1.0 canceled: context deadline exceeded');
  });
});

describe('Migrator wiring', () => {
  it('publishes on a kit bus under a custom prefix and keeps the version file layout', async () => {
    const bus = createBus();
    const seen: string[] = [];
    bus.subscribe('app.core.migration.#', (e) => {
      seen.push(e.topic);
    });
    const reg = new Registry();
    reg.register({ schema: 'cfg', version: '1.1.0', up: () => {} });
    let stored = '1.0.0';
    const m = new Migrator('t', 'v1.1.0', { registry: reg, dataDir: dir, publisher: bus, topicPrefix: 'app.core.migration' });
    m.addDriver({
      name: () => 'cfg',
      version: () => stored,
      setVersion: (v) => {
        stored = v;
      },
      backup: () => {},
      restore: () => {},
    });
    await m.run();
    expect(m.version).toBe('1.1.0');
    expect(seen).toEqual([
      'app.core.migration.run_started',
      'app.core.migration.started',
      'app.core.migration.applied',
      'app.core.migration.run_completed',
    ]);
    expect(await readVersionFile(dir, 'cfg')).toBe('1.1.0');
  });

  it('rollbackLatest is refused outside manual mode', async () => {
    const m = new Migrator('t', '1.0.0', { dataDir: dir });
    await expect(m.rollbackLatest()).rejects.toThrow('rollback: only available in manual rollback mode');
  });
});
