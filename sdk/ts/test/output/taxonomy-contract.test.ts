// Cross-language exit-taxonomy contract loader.
//
// Loads contracts/exit-taxonomy-v1/taxonomy.json — generated from
// go/console/output/envelope, the single source of truth — and asserts
// this TS SDK's table agrees with it on every class, every exit number
// and every transience.
//
// A failure here means Go's taxonomy moved and this port did not follow.
// That is the exact drift this contract exists to catch: when Go gained
// CONSENT_REFUSED 7 and PREREQUISITE 70, all four SDK ports silently
// kept the old nine-class table for two releases, and
// transienceForCode returned 'unknown' where Go returned 'transient' —
// so four runtimes handed agents wrong retry guidance.
//
// Fix by adding the missing class here, not by editing the contract:
// the contract is regenerated from Go, never hand-edited.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, it, expect } from 'vitest';

import {
  exitClasses,
  exitCodeForClass,
  transienceForCode,
} from '../../src/output/error';

interface TaxonomyClass {
  readonly class: string;
  readonly exit: number;
  readonly transience: string;
}

interface BandSlot {
  readonly exit: number;
  readonly class: string;
  readonly owner: string;
}

interface TaxonomyContract {
  readonly version: string;
  readonly source: string;
  readonly transiences: ReadonlyArray<string>;
  readonly classes: ReadonlyArray<TaxonomyClass>;
  readonly extension_band: ReadonlyArray<BandSlot>;
}

// Walk up from this test file's directory until we hit a directory
// holding contracts/exit-taxonomy-v1/taxonomy.json. Robust against both
// `vitest run` (CWD = sdk/ts) and IDE-driven invocations whose CWD may
// vary.
function locateContract(): string {
  const here = dirname(fileURLToPath(import.meta.url));
  let dir = here;
  for (let i = 0; i < 10; i++) {
    const candidate = resolve(
      dir,
      'contracts',
      'exit-taxonomy-v1',
      'taxonomy.json',
    );
    if (existsSync(candidate)) return candidate;
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `contracts/exit-taxonomy-v1/taxonomy.json: not found walking up from ${here}`,
  );
}

const CONTRACT: TaxonomyContract = JSON.parse(
  readFileSync(locateContract(), 'utf8'),
) as TaxonomyContract;

// The slots envelope itself owns. The conformance-tree slots (66-69)
// are recorded in the contract so a new allocation cannot double-book a
// number, but no SDK port is expected to export them.
const ENVELOPE_OWNER = 'hop.top/kit/go/console/output/envelope';

describe('exit-taxonomy-v1 contract metadata', () => {
  it('pins version and source', () => {
    expect(CONTRACT.version).toBe('v1');
    expect(CONTRACT.source).toBe(ENVELOPE_OWNER);
    expect(CONTRACT.classes.length).toBeGreaterThan(0);
  });
});

describe('exit-taxonomy-v1: this port matches the contract', () => {
  // Membership in both directions. A port carrying an extra class Go
  // does not define is as much a divergence as a port missing one: an
  // adopter branching on it gets a number no other runtime produces.
  it('declares exactly the contract classes, in contract order', () => {
    expect(exitClasses()).toEqual(CONTRACT.classes.map((c) => c.class));
  });

  it.each(CONTRACT.classes.map((c) => [c.class, c] as const))(
    '%s resolves to the contract exit and transience',
    (_name, row) => {
      expect(exitCodeForClass(row.class)).toBe(row.exit);
      expect(transienceForCode(row.class)).toBe(row.transience);
    },
  );

  // The transience vocabulary is itself a contract: an agent branching
  // on a fourth string has no defined behavior.
  it('answers only with the contract transience vocabulary', () => {
    for (const row of CONTRACT.classes) {
      expect(CONTRACT.transiences).toContain(transienceForCode(row.class));
    }
  });

  // An unknown class must resolve to nothing rather than to a fallback
  // number. A built-in fallback is what turns "kit added a class and
  // this port never copied it" into an assertion against exit 1 that
  // reads as a real failure rather than a stale table.
  it('does not invent a code for an adopter-defined class', () => {
    expect(exitCodeForClass('ADOPTER_SPECIFIC')).toBeUndefined();
    expect(transienceForCode('ADOPTER_SPECIFIC')).toBe('unknown');
  });
});

describe('exit-taxonomy-v1 extension band', () => {
  it('exports every band slot envelope owns', () => {
    const owned = CONTRACT.extension_band.filter(
      (s) => s.owner === ENVELOPE_OWNER,
    );
    expect(owned.length).toBeGreaterThan(0);
    for (const slot of owned) {
      expect(exitCodeForClass(slot.class)).toBe(slot.exit);
    }
  });

  // Slots owned elsewhere must NOT be claimed here. A port that
  // exported LEAK_DETECTED would be minting a number the conformance
  // tree owns, which is the collision the band record exists to stop.
  it('leaves slots owned by other packages unclaimed', () => {
    const foreign = CONTRACT.extension_band.filter(
      (s) => s.owner !== ENVELOPE_OWNER,
    );
    expect(foreign.length).toBeGreaterThan(0);
    for (const slot of foreign) {
      expect(exitCodeForClass(slot.class)).toBeUndefined();
    }
  });
});
