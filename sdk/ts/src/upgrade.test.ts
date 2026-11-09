import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import * as os from 'os';
import * as fs from 'fs';
import * as path from 'path';
import { createChecker, UpgradeCheckError } from './upgrade';

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

// ─── component tags and prereleases ──────────────────────────────────────────

interface Rel {
  tag_name: string;
  prerelease?: boolean;
  draft?: boolean;
}

const API = 'https://api.github.com/repos/acme/mono/releases';

/** Serve `pages[n-1]` for `?page=n` on the list endpoint; 404 elsewhere. */
function mockListing(pages: Rel[][]) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = new URL(String(input));
    if (url.origin + url.pathname !== API) return jsonResponse({}, 404);
    const page = Number(url.searchParams.get('page') ?? '1');
    return jsonResponse(pages[page - 1] ?? []);
  });
}

describe('createChecker: default mode', () => {
  let tmpDir: string;
  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'upgrade-test-'));
    vi.restoreAllMocks();
  });
  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
    vi.restoreAllMocks();
  });

  it('reads releases/latest only, with the latest-release link and bare cache', async () => {
    const spy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ tag_name: 'v2.0.0' }));
    const out = new StringStream();
    await createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'mono',
      stateDir: tmpDir,
    }).notifyIfAvailable(out as unknown as NodeJS.WritableStream);

    expect(spy).toHaveBeenCalledTimes(1);
    expect(String(spy.mock.calls[0][0])).toBe(`${API}/latest`);
    expect(out.value()).toBe(
      '\nA new release of my-tool is available: v1.0.0 → v2.0.0\n' +
        'https://github.com/acme/mono/releases/latest\n',
    );
    const cache = JSON.parse(
      fs.readFileSync(path.join(tmpDir, '.upgrade-my-tool-cache.json'), 'utf8'),
    );
    expect(Object.keys(cache)).toEqual(['version', 'checkedAt']);
  });

  it('stays silent for a component tag when no tagPrefix is set', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ tag_name: 'xray/v0.2.0' }),
    );
    const out = new StringStream();
    await createChecker({
      name: 'xray',
      currentVersion: '0.1.0',
      owner: 'acme',
      repo: 'mono',
      stateDir: tmpDir,
    }).notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    expect(out.value()).toBe('');
  });
});

describe('createChecker: tagPrefix and includePrereleases', () => {
  let tmpDir: string;
  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'upgrade-test-'));
    vi.restoreAllMocks();
  });
  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
    vi.restoreAllMocks();
  });

  async function run(
    currentVersion: string,
    extra: { tagPrefix?: string; includePrereleases?: boolean },
  ): Promise<string> {
    const out = new StringStream();
    await createChecker({
      name: 'xray',
      currentVersion,
      owner: 'acme',
      repo: 'mono',
      stateDir: tmpDir,
      ...extra,
    }).notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    return out.value();
  }

  it('notifies for a prefixed tag and links the exact release', async () => {
    mockListing([[{ tag_name: 'xray/v0.2.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe(
      '\nA new release of xray is available: v0.1.0 → v0.2.0\n' +
        'https://github.com/acme/mono/releases/tag/xray/v0.2.0\n',
    );
  });

  it('ignores other components even when their version is higher', async () => {
    mockListing([[{ tag_name: 'kit/v9.0.0' }, { tag_name: 'xray/v0.2.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toContain('v0.1.0 → v0.2.0');

    fs.rmSync(path.join(tmpDir, '.upgrade-xray-cache.json'));
    mockListing([[{ tag_name: 'kit/v9.0.0' }, { tag_name: 'xray-cli/v9.0.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
  });

  it('picks the highest semver, not the first listed', async () => {
    mockListing([
      [{ tag_name: 'xray/v0.2.1' }, { tag_name: 'xray/v0.10.0' }, { tag_name: 'xray/v0.9.0' }],
    ]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toContain('→ v0.10.0');
  });

  it('skips drafts', async () => {
    mockListing([[{ tag_name: 'xray/v0.3.0', draft: true }, { tag_name: 'xray/v0.2.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toContain('→ v0.2.0');
  });

  it('does not nudge a stable user onto a prerelease without includePrereleases', async () => {
    mockListing([
      [
        { tag_name: 'xray/v0.3.0-rc.1', prerelease: true },
        { tag_name: 'xray/v0.3.0-rc.0' }, // not flagged by GitHub; semver says prerelease
      ],
    ]);
    expect(await run('0.2.0', { tagPrefix: 'xray/v' })).toBe('');
  });

  it('offers a newer prerelease to a stable user when includePrereleases is on', async () => {
    mockListing([[{ tag_name: 'xray/v0.3.0-rc.1', prerelease: true }]]);
    expect(await run('0.2.0', { tagPrefix: 'xray/v', includePrereleases: true })).toContain(
      'v0.2.0 → v0.3.0-rc.1',
    );
  });

  it('shows a prerelease user the newer prerelease', async () => {
    mockListing([
      [
        { tag_name: 'xray/v0.1.0-rc.1', prerelease: true },
        { tag_name: 'xray/v0.1.0-rc.0', prerelease: true },
      ],
    ]);
    const o = { tagPrefix: 'xray/v', includePrereleases: true };
    expect(await run('0.1.0-rc.0', o)).toContain('v0.1.0-rc.0 → v0.1.0-rc.1');
    fs.rmSync(path.join(tmpDir, '.upgrade-xray-cache.json'));
    expect(await run('0.1.0-rc.1', o)).toBe('');
  });

  it('shows a prerelease user the final stable over its own prereleases', async () => {
    mockListing([
      [
        { tag_name: 'xray/v0.1.0-rc.1', prerelease: true },
        { tag_name: 'xray/v0.1.0' },
      ],
    ]);
    expect(
      await run('0.1.0-rc.0', { tagPrefix: 'xray/v', includePrereleases: true }),
    ).toContain('v0.1.0-rc.0 → v0.1.0\n');
  });

  it('shows a prerelease user the final stable without includePrereleases', async () => {
    mockListing([
      [
        { tag_name: 'xray/v0.1.0-rc.1', prerelease: true },
        { tag_name: 'xray/v0.1.0' },
      ],
    ]);
    expect(await run('0.1.0-rc.0', { tagPrefix: 'xray/v' })).toContain('v0.1.0-rc.0 → v0.1.0\n');
  });

  it('includePrereleases alone lists releases and links the tag as published', async () => {
    mockListing([[{ tag_name: 'v1.0.0-rc.1', prerelease: true }, { tag_name: 'v0.9.0' }]]);
    expect(await run('0.9.0', { includePrereleases: true })).toBe(
      '\nA new release of xray is available: v0.9.0 → v1.0.0-rc.1\n' +
        'https://github.com/acme/mono/releases/tag/v1.0.0-rc.1\n',
    );
  });

  it('never calls releases/latest in list mode', async () => {
    const spy = mockListing([[{ tag_name: 'xray/v0.2.0' }]]);
    await run('0.1.0', { tagPrefix: 'xray/v' });
    for (const call of spy.mock.calls) {
      expect(String(call[0])).not.toContain('/releases/latest');
    }
  });

  it('pages until a short page', async () => {
    const filler = (n: number): Rel[] =>
      Array.from({ length: 100 }, (_, i) => ({ tag_name: `kit/v1.${n}.${i}` }));
    const spy = mockListing([filler(0), [{ tag_name: 'xray/v0.2.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toContain('→ v0.2.0');
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('fetches at most 3 pages', async () => {
    const filler = (n: number): Rel[] =>
      Array.from({ length: 100 }, (_, i) => ({ tag_name: `kit/v1.${n}.${i}` }));
    const spy = mockListing([filler(0), filler(1), filler(2), [{ tag_name: 'xray/v0.2.0' }]]);
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
    expect(spy).toHaveBeenCalledTimes(3);
  });

  it('keeps earlier pages when a later page fails', async () => {
    const full = Array.from({ length: 100 }, (_, i) => ({ tag_name: `xray/v0.2.${i}` }));
    let n = 0;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      ++n === 1 ? jsonResponse(full) : jsonResponse({}, 500),
    );
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toContain('→ v0.2.99');
  });

  it('is silent when listing fails, returns junk, or throws', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}, 403));
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ message: 'nope' }));
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse([null, { tag_name: 7 }, { tag_name: 'xray/vbogus' }]),
    );
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('offline'));
    expect(await run('0.1.0', { tagPrefix: 'xray/v' })).toBe('');
  });

  it('keeps the tag link when the result comes from a fresh cache', async () => {
    const spy = mockListing([[{ tag_name: 'xray/v0.2.0' }]]);
    await run('0.1.0', { tagPrefix: 'xray/v' });
    const second = await run('0.1.0', { tagPrefix: 'xray/v' });
    expect(spy).toHaveBeenCalledTimes(1);
    expect(second).toContain('releases/tag/xray/v0.2.0');
  });
  it.each([
    ['path traversal', 'xray/v/../../../../evil/repo/releases/tag/x'],
    ['another version', 'xray/v9.9.9'],
    ['another component', 'kit/v2.0.0'],
    ['not a string', 42],
    ['missing', undefined],
  ])('treats a cached tag that does not name the version as a miss: %s', async (_label, tag) => {
    fs.writeFileSync(
      path.join(tmpDir, '.upgrade-xray-cache.json'),
      JSON.stringify({ version: '2.0.0', checkedAt: Date.now(), tag }),
    );
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse([{ tag_name: 'xray/v1.5.0', draft: false, prerelease: false }]),
    );
    const out = new StringStream();
    await createChecker({
      name: 'xray',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'mono',
      stateDir: tmpDir,
      tagPrefix: 'xray/v',
      cacheTTL: 60_000,
    }).notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    expect(spy).toHaveBeenCalled();
    expect(out.value()).toContain('https://github.com/acme/mono/releases/tag/xray/v1.5.0');
    expect(out.value()).not.toContain('evil');
  });

  it('uses a cached tag that names the version, without fetching', async () => {
    fs.writeFileSync(
      path.join(tmpDir, '.upgrade-xray-cache.json'),
      JSON.stringify({ version: '2.0.0', checkedAt: Date.now(), tag: 'xray/v2.0.0' }),
    );
    const spy = vi.spyOn(globalThis, 'fetch');
    const out = new StringStream();
    await createChecker({
      name: 'xray',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'mono',
      stateDir: tmpDir,
      tagPrefix: 'xray/v',
      cacheTTL: 60_000,
    }).notifyIfAvailable(out as unknown as NodeJS.WritableStream);
    expect(spy).not.toHaveBeenCalled();
    expect(out.value()).toContain('https://github.com/acme/mono/releases/tag/xray/v2.0.0');
  });
});

describe('createChecker: check()', () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'upgrade-check-'));
    vi.restoreAllMocks();
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
    vi.restoreAllMocks();
  });

  const mk = (extra: Record<string, unknown> = {}) =>
    createChecker({
      name: 'my-tool',
      currentVersion: '1.0.0',
      owner: 'acme',
      repo: 'my-tool',
      stateDir: tmpDir,
      ...extra,
    });

  it('resolves update info when newer', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tag_name: 'v2.0.0' }));
    expect(await mk().check()).toEqual({
      current: '1.0.0',
      latest: '2.0.0',
      updateAvailable: true,
      url: 'https://github.com/acme/my-tool/releases/latest',
    });
  });

  it('resolves updateAvailable false when up to date', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tag_name: 'v1.0.0' }));
    const r = await mk().check();
    expect(r.updateAvailable).toBe(false);
    expect(r.latest).toBe('1.0.0');
  });

  it('rejects with UpgradeCheckError on network failure', async () => {
    const boom = new Error('offline');
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(boom);
    const err = await mk().check().catch((e) => e);
    expect(err).toBeInstanceOf(UpgradeCheckError);
    expect(err.cause).toBe(boom);
  });

  it('rejects on HTTP error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({}, 500));
    await expect(mk().check()).rejects.toBeInstanceOf(UpgradeCheckError);
  });

  it('rejects on malformed body', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('not json', { status: 200 }),
    );
    await expect(mk().check()).rejects.toBeInstanceOf(UpgradeCheckError);
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ tag_name: 5 }));
    await expect(mk().check()).rejects.toBeInstanceOf(UpgradeCheckError);
  });

  it('rejects when no release matches tagPrefix', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse([{ tag_name: 'other/v9.0.0' }]),
    );
    await expect(mk({ tagPrefix: 'xray/v' }).check()).rejects.toBeInstanceOf(
      UpgradeCheckError,
    );
  });

  it('honours tagPrefix and links the exact release', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse([{ tag_name: 'xray/v1.5.0' }, { tag_name: 'other/v9.0.0' }]),
    );
    const r = await mk({ tagPrefix: 'xray/v' }).check();
    expect(r.latest).toBe('1.5.0');
    expect(r.url).toBe('https://github.com/acme/my-tool/releases/tag/xray/v1.5.0');
  });

  it('serves from cache within TTL; cacheTTL 0 bypasses it', async () => {
    const spy = vi
      .spyOn(globalThis, 'fetch')
      .mockImplementation(async () => jsonResponse({ tag_name: 'v2.0.0' }));
    await mk().check();
    await mk().check();
    expect(spy).toHaveBeenCalledTimes(1);
    await mk({ cacheTTL: 0 }).check();
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('notifyIfAvailable still swallows what check() rejects', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('offline'));
    const out = new StringStream();
    await expect(
      mk().notifyIfAvailable(out as unknown as NodeJS.WritableStream),
    ).resolves.toBeUndefined();
    expect(out.value()).toBe('');
  });
});
