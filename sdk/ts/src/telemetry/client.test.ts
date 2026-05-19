import {
  describe,
  it,
  expect,
  beforeEach,
  afterEach,
  vi,
} from 'vitest';
import { promises as fs } from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { Client } from './client';
import { consentPath } from './consent';
import { installIdPath, resetForTest } from './installId';

// Each test gets its own XDG roots so we never touch the real
// ~/.config/kit/config.yaml or ~/.local/state/kit/telemetry tree.
let tmpRoot: string;
let savedConfigHome: string | undefined;
let savedStateHome: string | undefined;
let savedMode: string | undefined;
let savedEndpoint: string | undefined;
let savedSink: string | undefined;
let savedSinkFile: string | undefined;
let savedQueueSize: string | undefined;

beforeEach(async () => {
  savedConfigHome = process.env.XDG_CONFIG_HOME;
  savedStateHome = process.env.XDG_STATE_HOME;
  savedMode = process.env.KIT_TELEMETRY_MODE;
  savedEndpoint = process.env.KIT_TELEMETRY_ENDPOINT;
  savedSink = process.env.KIT_TELEMETRY_SINK;
  savedSinkFile = process.env.KIT_TELEMETRY_SINK_FILE;
  savedQueueSize = process.env.KIT_TELEMETRY_QUEUE_SIZE;

  tmpRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kit-telemetry-client-'));
  process.env.XDG_CONFIG_HOME = path.join(tmpRoot, 'config');
  process.env.XDG_STATE_HOME = path.join(tmpRoot, 'state');
  delete process.env.KIT_TELEMETRY_MODE;
  delete process.env.KIT_TELEMETRY_ENDPOINT;
  delete process.env.KIT_TELEMETRY_SINK;
  delete process.env.KIT_TELEMETRY_SINK_FILE;
  delete process.env.KIT_TELEMETRY_QUEUE_SIZE;

  await resetForTest();
});

afterEach(async () => {
  function restore(key: string, val: string | undefined): void {
    if (val === undefined) delete process.env[key];
    else process.env[key] = val;
  }
  restore('XDG_CONFIG_HOME', savedConfigHome);
  restore('XDG_STATE_HOME', savedStateHome);
  restore('KIT_TELEMETRY_MODE', savedMode);
  restore('KIT_TELEMETRY_ENDPOINT', savedEndpoint);
  restore('KIT_TELEMETRY_SINK', savedSink);
  restore('KIT_TELEMETRY_SINK_FILE', savedSinkFile);
  restore('KIT_TELEMETRY_QUEUE_SIZE', savedQueueSize);
  await fs.rm(tmpRoot, { recursive: true, force: true });
  vi.restoreAllMocks();
});

async function grantConsent(): Promise<void> {
  const p = consentPath();
  await fs.mkdir(path.dirname(p), { recursive: true, mode: 0o700 });
  await fs.writeFile(
    p,
    [
      'kit:',
      '  telemetry:',
      '    consent:',
      '      state: granted',
      '      prompt_version: 1',
      '      decision_source: prompt',
      '      decided_at: 2026-05-19T12:00:00Z',
    ].join('\n'),
    { mode: 0o600 },
  );
}

async function denyConsent(): Promise<void> {
  const p = consentPath();
  await fs.mkdir(path.dirname(p), { recursive: true, mode: 0o700 });
  await fs.writeFile(
    p,
    'kit:\n  telemetry:\n    consent:\n      state: denied\n',
    { mode: 0o600 },
  );
}

describe('Client.record gating', () => {
  it('no-ops when consent is denied', async () => {
    await denyConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });
    c.record('test.event', { foo: 'bar' });
    await c.shutdown(500);
    await expect(fs.access(sinkFile)).rejects.toThrow();
  });

  it('no-ops when consent file is missing', async () => {
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });
    c.record('test.event');
    await c.shutdown(500);
    await expect(fs.access(sinkFile)).rejects.toThrow();
  });

  it('no-ops when mode is off (default)', async () => {
    await grantConsent();
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });
    c.record('test.event');
    await c.shutdown(500);
    await expect(fs.access(sinkFile)).rejects.toThrow();
  });

  it('returns synchronously under saturation (avg << 1ms)', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile, queueSize: 4 });
    // Hot loop: amortised cost must be non-blocking. We measure the
    // average rather than each iteration to tolerate GC pauses and
    // YAML-parse jitter on the consent fast-path.
    const start = process.hrtime.bigint();
    const N = 1000;
    for (let i = 0; i < N; i += 1) c.record('hot', { i });
    const totalMs = Number(process.hrtime.bigint() - start) / 1e6;
    expect(totalMs / N).toBeLessThan(1);
    await c.shutdown(2_000);
  });

  it('bumps droppedCount when the queue is full', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    // Tiny queue + a sink that doesn't drain (we never call shutdown
    // until the end) → guaranteed saturation.
    const c = new Client({ sink: 'jsonl', sinkFile, queueSize: 2 });
    c.record('e', { i: 1 });
    c.record('e', { i: 2 });
    // Queue full now.
    c.record('e', { i: 3 });
    c.record('e', { i: 4 });
    expect(c.droppedCount).toBeGreaterThanOrEqual(2);
    await c.shutdown(2_000);
  });
});

describe('Client.jsonl sink', () => {
  it('writes N envelopes to the file, one JSON object per line', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile, sdkVersion: '9.9.9' });

    c.record('a.event', { k: 1 });
    c.record('b.event', { k: 2 });
    c.record('c.event', { k: 3 });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const lines = raw.trim().split('\n');
    expect(lines).toHaveLength(3);
    for (const line of lines) {
      const env = JSON.parse(line);
      expect(env.schema_version).toBe('1');
      expect(env.sdk_lang).toBe('ts');
      expect(env.sdk_version).toBe('9.9.9');
      expect(env.installation_id).toMatch(/^[0-9a-f]{64}$/);
      expect(env.mode).toBe('anon');
      expect(typeof env.occurred_at).toBe('string');
      expect(typeof env.event).toBe('string');
      expect(typeof env.attrs).toBe('object');
    }
    // Ensure install_id file was created.
    const stat = await fs.stat(installIdPath());
    expect(stat.size).toBe(32);
  });

  it('applies the custom redactor BEFORE the default redact pass', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'full';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({
      sink: 'jsonl',
      sinkFile,
      redactor: (attrs) => ({ ...attrs, custom: 'TOUCHED' }),
    });

    c.record('e', { secret: 'sk-deadbeef12345678', plain: 'hi' });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const env = JSON.parse(raw.trim());
    expect(env.attrs.custom).toBe('TOUCHED');
    expect(env.attrs.secret).toBe('<redacted:token>');
    expect(env.attrs.plain).toBe('hi');
  });
});

describe('Client.https sink', () => {
  it('POSTs NDJSON and accepts a 2xx response', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';

    const calls: Array<{ url: string; body: string }> = [];
    const mockFetch = vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, body: String(init.body) });
      return new Response(null, { status: 202 });
    });
    vi.stubGlobal('fetch', mockFetch);

    const c = new Client({ sink: 'https', endpoint: 'https://t.example/ingest' });
    c.record('a', { i: 1 });
    c.record('b', { i: 2 });
    await c.shutdown(2_000);

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(calls[0].url).toBe('https://t.example/ingest');
    const lines = calls[0].body.trim().split('\n');
    expect(lines).toHaveLength(2);
    for (const line of lines) {
      const env = JSON.parse(line);
      expect(env.schema_version).toBe('1');
      expect(env.sdk_lang).toBe('ts');
    }
  });

  it('retries once on 5xx, then drops', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';

    const mockFetch = vi.fn(async () => new Response(null, { status: 503 }));
    vi.stubGlobal('fetch', mockFetch);

    const c = new Client({ sink: 'https', endpoint: 'https://t.example/ingest' });
    c.record('e', {});
    await c.shutdown(2_000);

    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(c.droppedCount).toBeGreaterThanOrEqual(1);
  });

  it('drops events when no endpoint is configured', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';

    const mockFetch = vi.fn();
    vi.stubGlobal('fetch', mockFetch);

    const c = new Client({ sink: 'https' });
    c.record('e', {});
    await c.shutdown(2_000);

    expect(mockFetch).not.toHaveBeenCalled();
    expect(c.droppedCount).toBeGreaterThanOrEqual(1);
  });
});

describe('Client anon mode strips attrs', () => {
  it('drops attrs payload to null in anon mode even with obvious PII', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });
    c.record('user.signup', {
      email: 'alice@example.com',
      ip: '192.168.1.42',
      token: 'sk-deadbeef12345678',
      note: 'totally not PII',
    });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const env = JSON.parse(raw.trim());
    expect(env.mode).toBe('anon');
    // `attrs` key survives (shape stability) but payload is null. Matches
    // the rs SDK's Value::Null strip for cross-lang byte-shape parity.
    expect('attrs' in env).toBe(true);
    expect(env.attrs).toBeNull();
  });

  it('preserves attrs in full mode (regression guard)', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'full';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });
    c.record('user.signup', { plan: 'pro', seats: 3 });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const env = JSON.parse(raw.trim());
    expect(env.mode).toBe('full');
    expect(env.attrs).toEqual({ plan: 'pro', seats: 3 });
  });

  it('bypasses the custom redactor in anon mode (PII cannot be reinjected)', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');

    const calls: Array<Record<string, unknown>> = [];
    const c = new Client({
      sink: 'jsonl',
      sinkFile,
      // A custom redactor that tries to inject PII back. In anon mode
      // we must skip it entirely so the attempt can't land.
      redactor: (attrs) => {
        calls.push(attrs);
        return { ...attrs, smuggled: 'sk-deadbeef12345678' };
      },
    });
    c.record('evt', { email: 'alice@example.com' });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const env = JSON.parse(raw.trim());
    expect(env.attrs).toBeNull();
    // Redactor was never invoked in anon mode.
    expect(calls).toHaveLength(0);
  });
});

describe('Client.shutdown', () => {
  it('drains queued events within the timeout window', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const sinkFile = path.join(tmpRoot, 'events.jsonl');
    const c = new Client({ sink: 'jsonl', sinkFile });

    for (let i = 0; i < 10; i += 1) c.record('drain', { i });
    await c.shutdown(2_000);

    const raw = await fs.readFile(sinkFile, 'utf8');
    const lines = raw.trim().split('\n');
    expect(lines).toHaveLength(10);
  });

  it('is idempotent', async () => {
    await grantConsent();
    process.env.KIT_TELEMETRY_MODE = 'anon';
    const c = new Client({
      sink: 'jsonl',
      sinkFile: path.join(tmpRoot, 'events.jsonl'),
    });
    await c.shutdown(500);
    await expect(c.shutdown(500)).resolves.toBeUndefined();
  });
});
