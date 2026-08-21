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
import {
  legacyLockBridge,
  modernLockBridge,
  mrtrLockBridge,
} from './testtree.js';

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

/**
 * The multi-round-trip confirmation exchange. Unlike `cases` and
 * `sequences` this is not byte-exact end to end: round 1 mints a
 * fresh, time-bound `requestState` whose MAC differs every run, so
 * only its framing is checkable. Round 2 echoes that state back and IS
 * byte-exact.
 */
interface WireMRTR {
  why?: string;
  confirmation_key: string;
  round1_headers: Record<string, string>;
  round1_request: string;
  round1_status: number;
  round1_must_have: Record<string, string>;
  round1_must_not_have: string[];
  state_framing: string;
  round2_headers: Record<string, string>;
  round2_request_template: string;
  round2_status: number;
  round2_response: string;
}

interface WireDoc {
  command_tree: string;
  cases: WireCase[];
  sequences?: WireSequence[];
  mrtr?: WireMRTR;
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

/**
 * The MRTR confirmation loop: the third fixture section, and the only
 * one that is not byte-exact end to end.
 *
 * Round 1 mints a fresh, time-bound `requestState` whose MAC differs
 * every run, so only its SHAPE is assertable — the fixture says which
 * members must be present and which must never appear. Round 2 echoes
 * that state back into a template and IS byte-exact, which is what
 * makes the whole exchange verifiable rather than merely plausible: a
 * port that fabricated a plausible-looking round 1 could not produce a
 * state its own round 2 accepts AND land on Go's exact bytes.
 *
 * Both rounds run against ONE mount, keyed with the fixture's
 * `confirmation_key`. The state is a MAC over that key, so a mount with
 * a different key cannot replay round 2.
 */
describe('MCP wire conformance — MRTR confirmation loop', () => {
  const doc = loadFixtures();
  const mrtr = doc.mrtr;

  it('loads the mrtr section', () => {
    expect(mrtr).toBeDefined();
  });

  it(`mrtr round trip — ${mrtr?.why ?? ''}`, async () => {
    if (mrtr === undefined) throw new Error('fixture has no mrtr section');

    const { bridge, executions } = mrtrLockBridge();
    const handler = createMcpHandler(bridge, {
      confirmationKey: mrtr.confirmation_key,
    });

    // --- Round 1: the prompt -------------------------------------------
    const r1 = await handler({
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...mrtr.round1_headers,
      },
      body: mrtr.round1_request,
    });
    expect(r1.status, 'round1 status').toBe(mrtr.round1_status);

    const body1 = JSON.parse(r1.body) as {
      result?: Record<string, unknown>;
    };
    const res1 = body1.result;
    expect(res1, 'round1 carries a result').toBeDefined();
    if (res1 === undefined) return;

    // The leaf must NOT have run: that is the entire defect this
    // section exists to catch. A port gating on X-Confirm-Token alone
    // would have refused at 428 above; a port with no gate at all would
    // have executed here.
    expect(executions(), 'leaf executed before confirmation').toBe(0);

    for (const [path, want] of Object.entries(mrtr.round1_must_have)) {
      expect(dig(res1, path), `round1 ${path}`).toBe(want);
    }
    for (const absent of mrtr.round1_must_not_have) {
      expect(absent in res1, `round1 must not carry ${absent}`).toBe(false);
    }

    // Exactly one entry, under the reserved "confirm" key.
    const requests = res1.inputRequests as Record<string, unknown>;
    expect(Object.keys(requests), 'inputRequests keys').toEqual(['confirm']);

    // `v1.<expiry-base10>.<mac>` — three dot-separated parts. The MAC
    // is production-derived and never compared.
    const state = res1.requestState;
    expect(typeof state, 'requestState is a string').toBe('string');
    const parts = String(state).split('.');
    expect(parts.length, 'requestState part count').toBe(3);
    expect(parts[0], 'requestState version').toBe('v1');
    expect(parts[1], 'requestState expiry is base-10').toMatch(/^\d+$/);
    expect(parts[2].length, 'requestState mac is non-empty').toBeGreaterThan(0);

    // --- Round 2: the accepted retry, byte-exact ------------------------
    const r2 = await handler({
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...mrtr.round2_headers,
      },
      body: mrtr.round2_request_template.replace(
        '{{requestState}}',
        String(state),
      ),
    });
    expect(r2.status, 'round2 status').toBe(mrtr.round2_status);
    expect(r2.body, 'round2 body').toBe(mrtr.round2_response);
    expect(executions(), 'executions after accept').toBe(1);
  });
});

/** Reads a dotted path out of a decoded result, for the fixture's assertions. */
function dig(root: Record<string, unknown>, path: string): unknown {
  let cur: unknown = root;
  for (const seg of path.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}
