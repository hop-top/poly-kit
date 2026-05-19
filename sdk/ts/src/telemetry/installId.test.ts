import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { promises as fs } from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import {
  getInstallId,
  rotate,
  resetForTest,
  installIdPath,
} from './installId';

// Each test gets its own XDG_STATE_HOME pointed at a fresh temp dir
// so we never touch the real ~/.local/state/kit/telemetry tree.
let tmpRoot: string;
let savedStateHome: string | undefined;

beforeEach(async () => {
  savedStateHome = process.env.XDG_STATE_HOME;
  tmpRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kit-telemetry-installid-'));
  process.env.XDG_STATE_HOME = tmpRoot;
});

afterEach(async () => {
  if (savedStateHome === undefined) {
    delete process.env.XDG_STATE_HOME;
  } else {
    process.env.XDG_STATE_HOME = savedStateHome;
  }
  await fs.rm(tmpRoot, { recursive: true, force: true });
});

describe('installIdPath', () => {
  it('resolves under XDG_STATE_HOME/kit/telemetry/installation_id', () => {
    expect(installIdPath()).toBe(
      path.join(tmpRoot, 'kit', 'telemetry', 'installation_id'),
    );
  });
});

describe('getInstallId', () => {
  it('generates a 64-char lowercase hex string on first call', async () => {
    const id = await getInstallId();
    expect(id).toMatch(/^[0-9a-f]{64}$/);
  });

  it('writes exactly 32 bytes to disk with mode 0600', async () => {
    await getInstallId();
    const stat = await fs.stat(installIdPath());
    expect(stat.size).toBe(32);
    // On POSIX, mask off the high bits; on win32 perms are advisory
    // so just check we wrote *something* reasonable.
    if (process.platform !== 'win32') {
      expect(stat.mode & 0o777).toBe(0o600);
    }
  });

  it('creates the parent directory with mode 0700', async () => {
    await getInstallId();
    const stat = await fs.stat(path.dirname(installIdPath()));
    expect(stat.isDirectory()).toBe(true);
    if (process.platform !== 'win32') {
      expect(stat.mode & 0o777).toBe(0o700);
    }
  });

  it('returns the same hex on repeated calls (stable across reads)', async () => {
    const a = await getInstallId();
    const b = await getInstallId();
    const c = await getInstallId();
    expect(a).toBe(b);
    expect(b).toBe(c);
  });

  it('throws when the on-disk file has wrong size', async () => {
    // Write 16 bytes manually, then read should throw.
    const p = installIdPath();
    await fs.mkdir(path.dirname(p), { recursive: true, mode: 0o700 });
    await fs.writeFile(p, Buffer.alloc(16, 0xab), { mode: 0o600 });
    await expect(getInstallId()).rejects.toThrow(/wrong size 16 bytes/);
  });
});

describe('rotate', () => {
  it('replaces the persisted bytes with a fresh value', async () => {
    const first = await getInstallId();
    const rotated = await rotate();
    expect(rotated).toMatch(/^[0-9a-f]{64}$/);
    expect(rotated).not.toBe(first);
    // Stable after rotate.
    const after = await getInstallId();
    expect(after).toBe(rotated);
  });

  it('works even when no file exists yet (cold rotate)', async () => {
    const id = await rotate();
    expect(id).toMatch(/^[0-9a-f]{64}$/);
    const stat = await fs.stat(installIdPath());
    expect(stat.size).toBe(32);
  });
});

describe('resetForTest', () => {
  it('deletes the persisted file', async () => {
    await getInstallId();
    await resetForTest();
    await expect(fs.access(installIdPath())).rejects.toThrow();
  });

  it('is idempotent (no error when file is already gone)', async () => {
    await resetForTest();
    await expect(resetForTest()).resolves.toBeUndefined();
  });

  it('regenerates a fresh ID after reset', async () => {
    const a = await getInstallId();
    await resetForTest();
    const b = await getInstallId();
    expect(b).toMatch(/^[0-9a-f]{64}$/);
    expect(b).not.toBe(a);
  });
});
