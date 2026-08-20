/**
 * Cross-language MCP wire conformance.
 *
 * Replays every case in sdk/tests/cross-lang/fixtures/mcp-wire.json —
 * generated from the live Go surface — against this port and asserts
 * the response is byte-identical and the HTTP status matches.
 *
 * Byte-identical means exactly that: the fixture body is compared to
 * the handler's body as a string, with NO JSON decode/re-encode on
 * either side. Go emits objects with lexicographically sorted keys
 * and a trailing newline; a serializer that differs must reorder to
 * match, not normalize the comparison away.
 *
 * The fixtures are the parity contract. Where ADR 0042/0043 and these
 * bytes disagree, the bytes win.
 */

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

import { createMcpHandler } from './dispatch.js';
import { legacyLockBridge, modernLockBridge } from './testtree.js';

interface WireCase {
  name: string;
  era: 'legacy' | 'modern';
  why?: string;
  mount?: string[];
  headers?: Record<string, string>;
  request: string;
  status: number;
  response: string;
}

interface WireDoc {
  command_tree: string;
  cases: WireCase[];
}

const FIXTURE_PATH = join(
  __dirname,
  '..',
  '..',
  '..',
  'tests',
  'cross-lang',
  'fixtures',
  'mcp-wire.json',
);

function loadFixtures(): WireDoc {
  return JSON.parse(readFileSync(FIXTURE_PATH, 'utf8')) as WireDoc;
}

/**
 * The Go generator drives two long-lived httptest servers — one over
 * the legacy lock tree for the legacy cases, one over the modern lock
 * tree for the modern ones. The trees differ (the legacy tree's
 * `widget add` carries extra `force`/`tag` flags), AND the servers
 * are stateful across cases: cobra attaches a command's `--help` flag
 * lazily on first execution, so `ping`'s schema grows a `help`
 * property once an earlier case has invoked it. The fixtures pin both
 * shapes of `ping`, so this replay mirrors the generator exactly —
 * one handler per era, cases in fixture order — rather than building
 * a fresh handler per case and normalizing the difference away.
 */
function handlersByEra() {
  return {
    legacy: createMcpHandler(legacyLockBridge()),
    modern: createMcpHandler(modernLockBridge()),
  };
}

describe('MCP wire conformance (cross-language fixtures)', () => {
  const doc = loadFixtures();

  it('loads the full fixture set', () => {
    expect(doc.cases.length).toBe(17);
  });

  // One handler per era, shared across cases and replayed in fixture
  // order — the generator's own execution model.
  const handlers = handlersByEra();
  const results: Array<{ c: WireCase; status: number; body: string }> = [];

  it('replays every case against its era handler', async () => {
    for (const c of doc.cases) {
      const handler = c.name.startsWith('legacy/')
        ? handlers.legacy
        : handlers.modern;
      const res = await handler({
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...(c.headers ?? {}) },
        body: c.request,
      });
      results.push({ c, status: res.status, body: res.body });
    }
    expect(results.length).toBe(doc.cases.length);
  });

  for (const [i, c] of doc.cases.entries()) {
    it(`${c.name} — ${c.why ?? ''}`, () => {
      const got = results[i];
      expect(got, `case ${c.name} was not replayed`).toBeDefined();
      // Status first: a status mismatch explains a body mismatch.
      expect(got.status, `status for ${c.name}`).toBe(c.status);
      // Byte-exact: raw string compare, no JSON round-trip.
      expect(got.body, `body for ${c.name}`).toBe(c.response);
    });
  }
});
