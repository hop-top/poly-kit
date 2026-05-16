import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import * as os from 'os';
import * as fs from 'fs';
import * as path from 'path';
import { createChecker } from './upgrade';

// ─── helpers ─────────────────────────────────────────────────────────────────

/** Writable stream that accumulates output into a string. */
class StringStream {
  private buf = '';

  write(chunk: string | Buffer): boolean {
    this.buf += typeof chunk === 'string' ? chunk : chunk.toString();
    return true;
  }

  value(): string {
    return this.buf;
  }
}

/** Build a minimal fetch Response with JSON body. */
function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

// ─── suite ───────────────────────────────────────────────────────────────────

describe('createChecker', () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'upgrade-test-'));
    vi.restoreAllMocks();
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
    vi.restoreAllMocks();
  });

  // ── newer version available ─────────────────────────────────────────────

  it('writes notice when newer version is available', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v2.0.0' }),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    expect(out.value()).toBe(
      '\nA new release of my-tool is available: v1.0.0 → v2.0.0\n' +
        'https://github.com/acme/my-tool/releases/latest\n',
    );
  });

  // ── same version → no output ────────────────────────────────────────────

  it('writes nothing when latest version equals current', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v1.0.0' }),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    expect(out.value()).toBe('');
  });

  // ── older remote version → no output ────────────────────────────────────

  it('writes nothing when latest version is older than current', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v0.9.0' }),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    expect(out.value()).toBe('');
  });

  // ── cache hit within TTL → fetch not called again ────────────────────────

  it('does not call fetch again when cache is fresh (within TTL)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v2.0.0' }),
    );

    const opts = {
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
      cacheTTL: 60_000, // 1 minute
    };

    const checker = createChecker(opts);
    const out = new StringStream();
    // First call — populates cache.
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    expect(fetchSpy).toHaveBeenCalledTimes(1);

    // Second call — should hit cache, not fetch.
    const out2 = new StringStream();
    await checker.notifyIfAvailable(out2 as unknown as NodeJS.WritableStream);
    expect(fetchSpy).toHaveBeenCalledTimes(1); // still 1
  });

  // ── cache miss (expired) → fetch called again ────────────────────────────

  it('calls fetch again when cache has expired', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v2.0.0' }),
    );

    const opts = {
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
      cacheTTL: 1, // 1 ms — expires almost immediately
    };

    const checker = createChecker(opts);
    const out = new StringStream();
    // First call — populates cache.
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    expect(fetchSpy).toHaveBeenCalledTimes(1);

    // Wait for TTL to expire.
    await new Promise((r) => setTimeout(r, 5));

    // Second call — cache expired, should fetch again.
    const out2 = new StringStream();
    await checker.notifyIfAvailable(out2 as unknown as NodeJS.WritableStream);
    expect(fetchSpy).toHaveBeenCalledTimes(2);
  });

  // ── fetch throws → silently skipped ─────────────────────────────────────

  it('silently skips and writes nothing when fetch throws', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('network error'));

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    // Must not throw.
    await expect(
      checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream),
    ).resolves.toBeUndefined();

    expect(out.value()).toBe('');
  });

  // ── notice format ────────────────────────────────────────────────────────

  it('formats the notice with correct owner/repo URL and versions', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v3.1.4' }),
    );

    const checker = createChecker({
      name: 'awesome-cli',
      currentVersion: '2.0.0',
      owner: 'myorg',
      repo: 'awesome-cli',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    const notice = out.value();
    expect(notice).toContain('awesome-cli');
    expect(notice).toContain('v2.0.0');
    expect(notice).toContain('v3.1.4');
    expect(notice).toContain('https://github.com/myorg/awesome-cli/releases/latest');
    // Exact format check.
    expect(notice).toBe(
      '\nA new release of awesome-cli is available: v2.0.0 → v3.1.4\n' +
        'https://github.com/myorg/awesome-cli/releases/latest\n',
    );
  });

  // ── tag_name without 'v' prefix ──────────────────────────────────────────

  it('handles tag_name without v prefix', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: '2.0.0' }),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    expect(out.value()).toContain('v2.0.0');
  });

  // ── non-200 HTTP response → silently skipped ─────────────────────────────

  it('silently skips on non-200 HTTP response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ message: 'Not Found' }, 404),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
    });

    const out = new StringStream();
    await expect(
      checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream),
    ).resolves.toBeUndefined();

    expect(out.value()).toBe('');
  });

  // ── pre-written stale cache on disk ─────────────────────────────────────

  it('reads cache from disk and skips fetch when fresh', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'v9.9.9' }),
    );

    // Write a fresh cache file manually.
    const cacheFile = path.join(tmpDir, '.upgrade-my-tool-cache.json');
    fs.writeFileSync(
      cacheFile,
      JSON.stringify({ version: '2.0.0', checkedAt: Date.now() }),
    );

    const checker = createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
      cacheTTL: 60_000,
    });

    const out = new StringStream();
    await checker.notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    // Should use cached version 2.0.0, not fetch.
    expect(fetchSpy).not.toHaveBeenCalled();
    // 2.0.0 > 1.0.0 so notice is shown.
    expect(out.value()).toContain('v2.0.0');
  });
});
