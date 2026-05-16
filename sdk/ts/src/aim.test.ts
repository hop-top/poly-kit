import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
import {
  parseQuery, normalize, Cache, Registry,
  ModelsDevSource,
  type Filter, type Model, type Provider, type Source,
} from './aim';

// ── Fixtures ───────────────────────────────────────────────────

const VECTORS_PATH = path.resolve(
  __dirname, '../test/testdata/query-vectors.json',
);
const FIXTURE_PATH = path.resolve(
  __dirname, '../test/testdata/api-fixture.json',
);

type Vector = {
  input: string;
  description: string;
  expected?: Record<string, unknown>;
  error?: string;
};

const vectors: Vector[] = JSON.parse(
  fs.readFileSync(VECTORS_PATH, 'utf-8'),
);
const fixtureData: Record<string, Provider> = JSON.parse(
  fs.readFileSync(FIXTURE_PATH, 'utf-8'),
);

// Map Go field names to TS field names for vector comparison.
function goFilterToTS(gf: Record<string, unknown>): Filter {
  const f: Filter = {};
  if (gf['Provider'] !== undefined) f.provider = gf['Provider'] as string;
  if (gf['Family'] !== undefined) f.family = gf['Family'] as string;
  if (gf['Input'] !== undefined) f.input = gf['Input'] as string;
  if (gf['Output'] !== undefined) f.output = gf['Output'] as string;
  if (gf['ToolCall'] !== undefined) f.toolCall = gf['ToolCall'] as boolean;
  if (gf['Reasoning'] !== undefined) f.reasoning = gf['Reasoning'] as boolean;
  if (gf['OpenWeights'] !== undefined)
    f.openWeights = gf['OpenWeights'] as boolean;
  if (gf['Query'] !== undefined) f.query = gf['Query'] as string;
  return f;
}

let tmpDir: string;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'aim-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

// ── parseQuery — test vectors ──────────────────────────────────

describe('parseQuery', () => {
  for (const v of vectors) {
    it(v.description, () => {
      if (v.error) {
        expect(() => parseQuery(v.input)).toThrow(v.error);
      } else {
        const got = parseQuery(v.input);
        const want = goFilterToTS(v.expected ?? {});
        expect(got).toEqual(want);
      }
    });
  }
});

// ── normalize ──────────────────────────────────────────────────

describe('normalize', () => {
  it('flattens modalities and limit', () => {
    const m = normalize({
      id: 'test', name: 'Test',
      modalities: { input: ['text'], output: ['image'] },
      limit: { context: 100000, output: 4096 },
    } as unknown as Model);
    expect(m.input).toEqual(['text']);
    expect(m.output).toEqual(['image']);
    expect(m.context).toBe(100000);
    expect(m.maxOutput).toBe(4096);
  });

  it('does not mutate the input object', () => {
    const orig = {
      id: 'test', name: 'Test',
      modalities: { input: ['text'], output: ['image'] },
      limit: { context: 100000, output: 4096 },
    } as unknown as Model;
    const before = JSON.stringify(orig);
    normalize(orig);
    expect(JSON.stringify(orig)).toBe(before);
  });

  it('defaults missing fields', () => {
    const m = normalize({ id: 'x', name: 'X' } as unknown as Model);
    expect(m.input).toEqual([]);
    expect(m.output).toEqual([]);
    expect(m.context).toBe(0);
    expect(m.maxOutput).toBe(0);
    expect(m.tool_call).toBe(false);
  });
});

// ── ModelsDevSource (mocked fetch) ─────────────────────────────

describe('ModelsDevSource', () => {
  it('fetches and normalizes providers', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      status: 200,
      text: () => Promise.resolve(JSON.stringify(fixtureData)),
      headers: new Map([['etag', '"abc"']]),
    });
    vi.stubGlobal('fetch', mockFetch);

    const src = new ModelsDevSource({ url: 'https://test.local/api.json' });
    const providers = await src.fetch();

    expect(Object.keys(providers)).toHaveLength(3);
    expect(providers['anthropic'].models['claude-3-5-sonnet'].input)
      .toEqual(['text', 'image']);
    expect(providers['anthropic'].models['claude-3-5-sonnet'].context)
      .toBe(200000);

    vi.unstubAllGlobals();
  });

  it('rejects status != 200', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 500 }));
    const src = new ModelsDevSource();
    await expect(src.fetch()).rejects.toThrow('unexpected status 500');
    vi.unstubAllGlobals();
  });

  it('rejects oversized response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      status: 200,
      text: () => Promise.resolve('x'.repeat(200)),
    }));
    const src = new ModelsDevSource({ maxSize: 100 });
    await expect(src.fetch()).rejects.toThrow('exceeds max size');
    vi.unstubAllGlobals();
  });
});

// ── Cache ──────────────────────────────────────────────────────

describe('Cache', () => {
  function mockSource(): Source {
    return {
      fetch: vi.fn().mockResolvedValue(fixtureData),
    };
  }

  it('caches to disk and serves from cache', async () => {
    const src = mockSource();
    const cache = new Cache(src, { dir: tmpDir, ttl: 60_000 });

    const first = await cache.fetch();
    expect(Object.keys(first)).toHaveLength(3);
    expect(src.fetch).toHaveBeenCalledTimes(1);

    const second = await cache.fetch();
    expect(Object.keys(second)).toHaveLength(3);
    expect(src.fetch).toHaveBeenCalledTimes(1); // still 1

    expect(fs.existsSync(path.join(tmpDir, 'providers.json'))).toBe(true);
  });

  it('refetches when TTL expired', async () => {
    const src = mockSource();
    const cache = new Cache(src, { dir: tmpDir, ttl: 1 }); // 1ms TTL

    await cache.fetch();
    await new Promise(r => setTimeout(r, 10));
    await cache.fetch();
    expect(src.fetch).toHaveBeenCalledTimes(2);
  });

  it('stale-on-error returns cached data', async () => {
    const src = mockSource();
    const cache = new Cache(src, { dir: tmpDir, ttl: 1 });

    await cache.fetch(); // populate cache
    (src.fetch as ReturnType<typeof vi.fn>)
      .mockRejectedValue(new Error('network'));

    await new Promise(r => setTimeout(r, 10));
    const result = await cache.fetch(); // should serve stale
    expect(Object.keys(result)).toHaveLength(3);
  });
});

// ── Registry ───────────────────────────────────────────────────

describe('Registry', () => {
  function fixtureSource(): Source {
    // normalize models in fixture
    const normalized = JSON.parse(JSON.stringify(fixtureData));
    for (const p of Object.values(normalized) as Provider[]) {
      for (const [id, m] of Object.entries(p.models)) {
        p.models[id] = normalize(m);
      }
    }
    return { fetch: vi.fn().mockResolvedValue(normalized) };
  }

  it('lists all providers sorted', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const providers = await reg.getProviders();
    expect(providers.map(p => p.id))
      .toEqual(['anthropic', 'meta', 'openai']);
  });

  it('filters by provider', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ provider: 'anthropic' });
    expect(models).toHaveLength(3);
    expect(models.every(m => m.provider === 'anthropic')).toBe(true);
  });

  it('filters by comma-separated provider', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ provider: 'anthropic,openai' });
    expect(models.length).toBeGreaterThan(0);
    expect(models.every(
      m => m.provider === 'anthropic' || m.provider === 'openai',
    )).toBe(true);
  });

  it('filters by reasoning:true', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ reasoning: true });
    const ids = models.map(m => m.id);
    expect(ids).toContain('claude-3-7-sonnet');
    expect(ids).toContain('o1');
    expect(ids).not.toContain('gpt-4o');
  });

  it('filters by input modality', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ input: 'image' });
    expect(models.every(m => m.input.includes('image'))).toBe(true);
  });

  it('filters by output modality', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ output: 'image' });
    expect(models).toHaveLength(1);
    expect(models[0].id).toBe('dall-e-3');
  });

  it('filters by openWeights', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ openWeights: true });
    expect(models.every(m => m.open_weights === true)).toBe(true);
    expect(models).toHaveLength(2);
  });

  it('filters by free-text query', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.models({ query: 'claude' });
    expect(models).toHaveLength(3);
  });

  it('get() returns specific model', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const m = await reg.get('openai', 'gpt-4o');
    expect(m).toBeDefined();
    expect(m!.name).toBe('GPT-4o');
  });

  it('get() returns undefined for missing', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    expect(await reg.get('openai', 'nonexistent')).toBeUndefined();
    expect(await reg.get('missing', 'gpt-4o')).toBeUndefined();
  });

  it('query() parses string and filters', async () => {
    const reg = new Registry({
      sources: [fixtureSource()], cacheDir: tmpDir,
    });
    const models = await reg.query('provider:openai reasoning:true');
    expect(models).toHaveLength(1);
    expect(models[0].id).toBe('o1');
  });

  it('merges multiple sources (last wins)', async () => {
    const src1: Source = {
      fetch: vi.fn().mockResolvedValue({
        anthropic: fixtureData['anthropic'],
      }),
    };
    const src2: Source = {
      fetch: vi.fn().mockResolvedValue({
        openai: fixtureData['openai'],
      }),
    };
    const reg = new Registry({
      sources: [src1, src2], cacheDir: tmpDir,
    });
    const providers = await reg.getProviders();
    expect(providers.map(p => p.id)).toEqual(['anthropic', 'openai']);
  });
});
