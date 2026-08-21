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

interface WireStep {
  name: string;
  request: string;
  status: number;
  response: string;
}

interface WireSequence {
  name: string;
  era: 'legacy' | 'modern';
  why?: string;
  mount?: string[];
  headers?: Record<string, string>;
  steps: WireStep[];
}

interface WireDoc {
  command_tree: string;
  cases: WireCase[];
  sequences?: WireSequence[];
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
 * The Go generator builds a fresh server per case, over one of two
 * command trees: the legacy lock tree for the legacy cases, the
 * modern lock tree for the modern ones. The trees differ — the
 * legacy tree's `widget add` carries extra `force`/`tag` flags the
 * modern tree omits — so the fixtures cannot be served from a single
 * shared tree. Each case is otherwise independent: no state carries
 * from one case to the next.
 */
function bridgeFor(c: { name: string }) {
  return c.name.startsWith('legacy/') ? legacyLockBridge() : modernLockBridge();
}

describe('MCP wire conformance (cross-language fixtures)', () => {
  const doc = loadFixtures();

  it('loads the full fixture set', () => {
    expect(doc.cases.length).toBe(18);
  });

  for (const c of doc.cases) {
    it(`${c.name} — ${c.why ?? ''}`, async () => {
      const handler = createMcpHandler(bridgeFor(c));
      const res = await handler({
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...(c.headers ?? {}) },
        body: c.request,
      });

      // Status first: a status mismatch explains a body mismatch.
      expect(res.status, `status for ${c.name}`).toBe(c.status);
      // Byte-exact: raw string compare, no JSON round-trip.
      expect(res.body, `body for ${c.name}`).toBe(c.response);
    });
  }
});

/**
 * Sequences pin behavior that no single request can express: state
 * that legitimately accumulates on a LONG-LIVED mount. Each sequence
 * gets ONE handler, and its steps are posted in order against it.
 *
 * This is the counterpart to `cases`, not a replacement: cases assert
 * that a request's response does not depend on request history, and
 * sequences assert that where Go's response DOES depend on history,
 * this port depends on it identically. Adopters serve from persistent
 * processes, so a port that rebuilt its command tree per request
 * would pass every case and still diverge here.
 */
describe('MCP wire conformance — sequences (one long-lived mount)', () => {
  const doc = loadFixtures();
  const sequences = doc.sequences ?? [];

  it('loads the full sequence set', () => {
    expect(sequences.length).toBe(1);
  });

  for (const seq of sequences) {
    it(`${seq.name} — ${seq.why ?? ''}`, async () => {
      // ONE handler for the whole sequence: the state under test is
      // exactly what survives between steps.
      const handler = createMcpHandler(bridgeFor(seq));

      for (const [i, step] of seq.steps.entries()) {
        const res = await handler({
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            ...(seq.headers ?? {}),
          },
          body: step.request,
        });

        const label = `${seq.name} step ${i} (${step.name})`;
        expect(res.status, `status for ${label}`).toBe(step.status);
        expect(res.body, `body for ${label}`).toBe(step.response);
      }
    });
  }
});
